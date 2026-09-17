package replication

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

// Wire frame types. Frames are [1 byte type][4 byte big-endian length][payload].
// After an InstallSnapshot request frame the snapshot data is streamed raw for
// exactly args.Size bytes, then the response frame follows. Raft protocol
// frames carry no per-message Gantry nonce/signature: the connection was
// authenticated at establishment and is bound to one node identity.
const (
	frameAppendEntries = 1 + iota
	frameRequestVote
	frameRequestPreVote
	frameInstallSnapshot
	frameTimeoutNow
	frameResponse
	frameAuth
	frameAuthResp
)

const maxFrameBytes = 64 << 20 // 64 MiB safety cap on a single frame

type wireResponse struct {
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PeerCredentialSource resolves the outbound credential to present when
// authenticating to a specific peer. Products with pairwise credentials (e.g.
// Watchpost stores a different cluster_members.outbound_secret per peer) must
// provide this; a single process-global secret cannot authenticate a genuine
// multi-peer cluster correctly.
type PeerCredentialSource interface {
	OutboundCredential(ctx context.Context, peer raft.ServerID) (string, error)
}

// NetTransportOptions configures a production Raft transport over
// Gantry-authenticated TCP connections.
type NetTransportOptions struct {
	ID              raft.ServerID
	Address         raft.ServerAddress   // advertised listen address
	Listener        net.Listener         // optional; defaults to tcp listen
	TLSConfig       *tls.Config          // required in production; protects the channel
	Authenticator   PeerAuthenticator    // binds connections to Gantry identity (both directions)
	Membership      MembershipChecker    // revalidation + capability source
	Secret          string               // static outbound secret (Memory/test transport only)
	PeerCredentials PeerCredentialSource // per-peer outbound credential resolver (production)
	Protocol        int
	Capabilities    Capabilities // advertised
	DialTimeout     time.Duration
	SendTimeout     time.Duration
	RevalidateEvery time.Duration

	// InsecureAllowPlaintext permits an unencrypted channel. It is an explicit
	// local/test mode only: production MUST supply TLSConfig. Mutual Gantry
	// identity binding still applies either way; this flag only disables
	// encryption, never authentication.
	InsecureAllowPlaintext bool
}

// outboundSecret resolves the credential to present to peer: the peer-aware
// source when configured, otherwise the static transport secret.
func (t *NetTransport) outboundSecret(ctx context.Context, peer raft.ServerID) (string, error) {
	if t.opts.PeerCredentials != nil {
		return t.opts.PeerCredentials.OutboundCredential(ctx, peer)
	}
	return t.secret, nil
}

// NetTransport implements raft.Transport over authenticated long-lived TCP
// connections. It is the production counterpart to the in-process test Fabric.
type NetTransport struct {
	opts       NetTransportOptions
	consumerCh chan raft.RPC
	listener   net.Listener
	localAddr  raft.ServerAddress
	secret     string

	mu       sync.Mutex
	conns    map[raft.ServerAddress]*peerConn
	peerIDs  map[raft.ServerAddress]raft.ServerID
	accepted map[net.Conn]struct{}
	closed   bool
	shutdown chan struct{}
	wg       sync.WaitGroup
}

var _ raft.Transport = (*NetTransport)(nil)

// handshakeResponse is the accepting side's reply: the auth decision plus the
// accepting node's OWN signed identity proof, so the dialer can authenticate
// the server (mutual Gantry authentication).
type handshakeResponse struct {
	Result     AuthResult   `json:"result"`
	ServerAuth *AuthRequest `json:"server_auth,omitempty"`
}

type peerConn struct {
	conn     net.Conn
	expected raft.ServerID
	mu       sync.Mutex
}

// NewNetTransport starts a NetTransport on opts.Address (or opts.Listener).
// An unencrypted channel is only permitted with an explicit opt-in.
func NewNetTransport(opts NetTransportOptions) (*NetTransport, error) {
	if opts.Authenticator == nil {
		return nil, errors.New("replication: net transport requires an authenticator")
	}
	if opts.TLSConfig == nil && !opts.InsecureAllowPlaintext {
		return nil, errors.New("replication: production transport requires TLS; plaintext requires explicit InsecureAllowPlaintext (local/test only)")
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 5 * time.Second
	}
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = 10 * time.Second
	}
	if opts.RevalidateEvery <= 0 {
		opts.RevalidateEvery = time.Second
	}
	if opts.Protocol <= 0 {
		opts.Protocol = Version
	}
	t := &NetTransport{
		opts:       opts,
		consumerCh: make(chan raft.RPC, 128),
		localAddr:  opts.Address,
		secret:     opts.Secret,
		conns:      make(map[raft.ServerAddress]*peerConn),
		peerIDs:    make(map[raft.ServerAddress]raft.ServerID),
		accepted:   make(map[net.Conn]struct{}),
		shutdown:   make(chan struct{}),
	}
	if opts.Listener != nil {
		t.listener = opts.Listener
	} else {
		ln, err := net.Listen("tcp", string(opts.Address))
		if err != nil {
			return nil, err
		}
		t.listener = ln
		// Advertise the actual bound address (e.g. 127.0.0.1:0 -> real port).
		t.localAddr = raft.ServerAddress(ln.Addr().String())
	}
	t.wg.Add(1)
	go t.serve()
	if opts.Membership != nil {
		t.wg.Add(1)
		go t.revalidateLoop()
	}
	return t, nil
}

// LocalAddr implements raft.Transport.
func (t *NetTransport) LocalAddr() raft.ServerAddress { return t.localAddr }

// Consumer implements raft.Transport.
func (t *NetTransport) Consumer() <-chan raft.RPC { return t.consumerCh }

// SetHeartbeatHandler is a no-op: heartbeats flow through the consumer.
func (t *NetTransport) SetHeartbeatHandler(func(rpc raft.RPC)) {}

// EncodePeer/DecodePeer implement raft.Transport (addresses are raw strings).
func (t *NetTransport) EncodePeer(id raft.ServerID, a raft.ServerAddress) []byte { return []byte(a) }
func (t *NetTransport) DecodePeer(b []byte) raft.ServerAddress                   { return raft.ServerAddress(b) }

// AppendEntriesPipeline is not supported; raft falls back to synchronous
// AppendEntries (correct, exercised in tests).
func (t *NetTransport) AppendEntriesPipeline(id raft.ServerID, target raft.ServerAddress) (raft.AppendPipeline, error) {
	return nil, raft.ErrPipelineReplicationNotSupported
}

// AppendEntries implements raft.Transport.
func (t *NetTransport) AppendEntries(id raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return t.roundTrip(id, target, frameAppendEntries, payload, resp)
}

// RequestVote implements raft.Transport.
func (t *NetTransport) RequestVote(id raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest, resp *raft.RequestVoteResponse) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return t.roundTrip(id, target, frameRequestVote, payload, resp)
}

// TimeoutNow implements raft.Transport.
func (t *NetTransport) TimeoutNow(id raft.ServerID, target raft.ServerAddress, args *raft.TimeoutNowRequest, resp *raft.TimeoutNowResponse) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return t.roundTrip(id, target, frameTimeoutNow, payload, resp)
}

// InstallSnapshot implements raft.Transport: request frame, then the snapshot
// data streamed raw for args.Size bytes, then the response.
func (t *NetTransport) InstallSnapshot(id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest, resp *raft.InstallSnapshotResponse, data io.Reader) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	pc, err := t.getConn(context.Background(), target, id)
	if err != nil {
		return err
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if err := writeFrame(pc.conn, frameInstallSnapshot, payload); err != nil {
		t.dropConn(target, pc)
		return err
	}
	if _, err := io.CopyN(pc.conn, data, args.Size); err != nil {
		t.dropConn(target, pc)
		return err
	}
	return t.readResponse(pc, target, resp)
}

// roundTrip sends a request frame and decodes the typed response.
func (t *NetTransport) roundTrip(id raft.ServerID, target raft.ServerAddress, typ byte, payload []byte, resp interface{}) error {
	pc, err := t.getConn(context.Background(), target, id)
	if err != nil {
		return err
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if err := writeFrame(pc.conn, typ, payload); err != nil {
		t.dropConn(target, pc)
		return err
	}
	return t.readResponse(pc, target, resp)
}

func (t *NetTransport) readResponse(pc *peerConn, target raft.ServerAddress, resp interface{}) error {
	typ, payload, err := readFrame(pc.conn)
	if err != nil {
		t.dropConn(target, pc)
		return err
	}
	if typ != frameResponse {
		t.dropConn(target, pc)
		return fmt.Errorf("replication: unexpected frame type %d", typ)
	}
	var wr wireResponse
	if err := json.Unmarshal(payload, &wr); err != nil {
		t.dropConn(target, pc)
		return err
	}
	if wr.Error != "" {
		return errors.New(wr.Error)
	}
	if len(wr.Payload) > 0 {
		if err := json.Unmarshal(wr.Payload, resp); err != nil {
			t.dropConn(target, pc)
			return err
		}
	}
	return nil
}

func (t *NetTransport) getConn(ctx context.Context, target raft.ServerAddress, expected raft.ServerID) (*peerConn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("replication: transport closed")
	}
	if pc, ok := t.conns[target]; ok {
		t.mu.Unlock()
		return pc, nil
	}
	t.mu.Unlock()

	pc, err := t.dial(ctx, target, expected)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		_ = pc.conn.Close()
		return nil, errors.New("replication: transport closed")
	}
	if existing, ok := t.conns[target]; ok {
		_ = pc.conn.Close()
		return existing, nil
	}
	t.conns[target] = pc
	t.peerIDs[target] = expected
	return pc, nil
}

func (t *NetTransport) dropConn(target raft.ServerAddress, pc *peerConn) {
	t.mu.Lock()
	if cur, ok := t.conns[target]; ok && cur == pc {
		delete(t.conns, target)
		delete(t.peerIDs, target)
	}
	t.mu.Unlock()
	_ = pc.conn.Close()
}

// dial opens a TCP (optionally TLS) connection and performs the mutual Gantry
// authenticated handshake: this node proves its identity to the peer, and the
// peer must prove it is exactly the expected node before the connection is
// used. On TLS, the channel is encrypted; node identity is always bound by the
// Gantry handshake in both directions.
func (t *NetTransport) dial(ctx context.Context, target raft.ServerAddress, expected raft.ServerID) (*peerConn, error) {
	// Revocation contract: this node does not send replication to a peer whose
	// local membership view is not an active, replication-authorized, compatible
	// participant. New connections to a disabled/revoked peer are refused here;
	// established connections are revalidated (and dropped) by the periodic
	// revalidation loop. Combined with the acceptor's membership check this
	// makes disable/revoke directional in BOTH directions: the peer neither
	// authenticates outward nor receives new committed state.
	if t.opts.Membership != nil {
		st, err := t.opts.Membership.Membership(ctx, expected)
		if err != nil || st.State != MembershipActive || !st.ReplicationEnabled || st.Protocol != t.opts.Protocol {
			return nil, fmt.Errorf("replication: peer %s is not an active replication member", expected)
		}
	}
	raw, err := net.DialTimeout("tcp", string(target), t.opts.DialTimeout)
	if err != nil {
		return nil, err
	}
	conn := net.Conn(raw)
	if t.opts.TLSConfig != nil {
		tc := tls.Client(conn, t.opts.TLSConfig)
		if err := tc.Handshake(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tc
	}
	nonce, err := newNonce()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	secret, err := t.outboundSecret(ctx, expected)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("replication: resolve outbound credential for %s: %w", expected, err)
	}
	req, err := buildHandshake(t.opts.ID, t.opts.ID, t.localAddr, t.opts.Protocol, t.opts.Capabilities, secret, nonce, time.Now())
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := writeFrame(conn, frameAuth, body); err != nil {
		_ = conn.Close()
		return nil, err
	}
	typ, payload, err := readFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if typ != frameAuthResp {
		_ = conn.Close()
		return nil, fmt.Errorf("replication: unexpected auth frame %d", typ)
	}
	var hr handshakeResponse
	if err := json.Unmarshal(payload, &hr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !hr.Result.Allowed {
		_ = conn.Close()
		return nil, fmt.Errorf("replication: handshake rejected: %s", hr.Result.Reason)
	}
	if hr.ServerAuth == nil {
		_ = conn.Close()
		return nil, errors.New("replication: accepting peer provided no identity proof (unauthenticated endpoint)")
	}
	// The peer must prove it is exactly the expected Gantry node / raft.ServerID.
	if err := t.opts.Authenticator.VerifyPeer(context.Background(), expected, *hr.ServerAuth); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("replication: peer authentication failed: %w", err)
	}
	return &peerConn{conn: conn, expected: expected}, nil
}

// serve accepts connections and hands each to serveConn.
func (t *NetTransport) serve() {
	defer t.wg.Done()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.shutdown:
				return
			default:
			}
			continue
		}
		t.wg.Add(1)
		go t.serveConn(conn)
	}
}

// serveConn authenticates the connection, then serves raft RPC frames.
func (t *NetTransport) serveConn(raw net.Conn) {
	defer t.wg.Done()
	t.mu.Lock()
	t.accepted[raw] = struct{}{}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.accepted, raw)
		t.mu.Unlock()
	}()
	defer raw.Close()
	conn := net.Conn(raw)
	if t.opts.TLSConfig != nil {
		tc := tls.Server(conn, t.opts.TLSConfig)
		if err := tc.Handshake(); err != nil {
			return
		}
		conn = tc
	}
	br := bufio.NewReader(conn)

	typ, payload, err := readFrame(br)
	if err != nil || typ != frameAuth {
		return
	}
	var req AuthRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}
	res, err := t.opts.Authenticator.Authenticate(context.Background(), req)
	hr := handshakeResponse{Result: AuthResult{Allowed: false, Reason: "authentication failed"}}
	if err == nil && res.Allowed {
		// Reciprocate: prove THIS node's Gantry identity to the dialer so
		// authentication is mutual. The reciprocal proof uses the credential for
		// the authenticated peer (pairwise credentials resolve per peer).
		secret, serr := t.outboundSecret(context.Background(), req.NodeID)
		if serr != nil {
			return
		}
		snonce, err := newNonce()
		if err != nil {
			return
		}
		sauth, err := buildHandshake(t.opts.ID, t.opts.ID, t.localAddr, t.opts.Protocol, t.opts.Capabilities, secret, snonce, time.Now())
		if err != nil {
			return
		}
		hr.Result = res
		hr.ServerAuth = sauth
	}
	out, _ := json.Marshal(hr)
	if err := writeFrame(conn, frameAuthResp, out); err != nil {
		return
	}
	if !hr.Result.Allowed {
		return
	}
	authedNode := req.NodeID

	// Server-side revalidation: terminate the connection if the authenticated
	// membership leaves active/replication-authorized state or the replication
	// protocol becomes incompatible. Operation-schema capability version
	// changes do NOT terminate the channel - they feed per-operation schema
	// gating instead.
	stop := make(chan struct{})
	defer close(stop)
	if t.opts.Membership != nil {
		go func() {
			tk := time.NewTicker(t.opts.RevalidateEvery)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					st, err := t.opts.Membership.Membership(context.Background(), authedNode)
					if err != nil || st.State != MembershipActive || !st.ReplicationEnabled || st.Protocol != t.opts.Protocol {
						_ = raw.Close()
						return
					}
				}
			}
		}()
	}

	for {
		typ, payload, err := readFrame(br)
		if err != nil {
			return
		}
		respCh := make(chan raft.RPCResponse, 1)
		rpc := raft.RPC{RespChan: respCh}
		switch typ {
		case frameAppendEntries:
			var cmd raft.AppendEntriesRequest
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return
			}
			rpc.Command = &cmd
		case frameRequestVote:
			var cmd raft.RequestVoteRequest
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return
			}
			rpc.Command = &cmd
		case frameRequestPreVote:
			var cmd raft.RequestPreVoteRequest
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return
			}
			rpc.Command = &cmd
		case frameInstallSnapshot:
			var cmd raft.InstallSnapshotRequest
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return
			}
			rpc.Command = &cmd
			rpc.Reader = io.LimitReader(br, cmd.Size)
		case frameTimeoutNow:
			var cmd raft.TimeoutNowRequest
			if err := json.Unmarshal(payload, &cmd); err != nil {
				return
			}
			rpc.Command = &cmd
		default:
			return
		}
		select {
		case t.consumerCh <- rpc:
		case <-t.shutdown:
			return
		}
		var resp raft.RPCResponse
		select {
		case resp = <-respCh:
		case <-t.shutdown:
			return
		}
		werr := ""
		if resp.Error != nil {
			werr = resp.Error.Error()
		}
		wp, err := json.Marshal(resp.Response)
		if err != nil {
			return
		}
		out, err := json.Marshal(wireResponse{Error: werr, Payload: wp})
		if err != nil {
			return
		}
		if err := writeFrame(conn, frameResponse, out); err != nil {
			return
		}
	}
}

// revalidateLoop periodically re-checks membership for established peers and
// closes connections whose peer left the active state, forcing re-handshake
// (which fails on revoked/disabled/incompatible peers).
func (t *NetTransport) revalidateLoop() {
	defer t.wg.Done()
	tk := time.NewTicker(t.opts.RevalidateEvery)
	defer tk.Stop()
	for {
		select {
		case <-t.shutdown:
			return
		case <-tk.C:
			t.mu.Lock()
			type targetPC struct {
				addr raft.ServerAddress
				pc   *peerConn
				id   raft.ServerID
			}
			var list []targetPC
			for addr, pc := range t.conns {
				list = append(list, targetPC{addr, pc, t.peerIDs[addr]})
			}
			t.mu.Unlock()
			for _, tp := range list {
				st, err := t.opts.Membership.Membership(context.Background(), tp.id)
				if err != nil || st.State != MembershipActive || !st.ReplicationEnabled || st.Protocol != t.opts.Protocol {
					t.dropConn(tp.addr, tp.pc)
				}
			}
		}
	}
}

// Close shuts the transport down, closing the listener and every accepted and
// dialed connection so no serve goroutine is left blocked.
func (t *NetTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := t.conns
	t.conns = make(map[raft.ServerAddress]*peerConn)
	accepted := make([]net.Conn, 0, len(t.accepted))
	for c := range t.accepted {
		accepted = append(accepted, c)
	}
	t.mu.Unlock()
	close(t.shutdown)
	_ = t.listener.Close()
	for _, pc := range conns {
		_ = pc.conn.Close()
	}
	for _, c := range accepted {
		_ = c.Close()
	}
	t.wg.Wait()
	return nil
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxFrameBytes {
		return errors.New("replication: frame too large")
	}
	hdr := make([]byte, 5)
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrameBytes {
		return 0, nil, errors.New("replication: frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
