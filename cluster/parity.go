package cluster

// Common cluster API and presentation vocabulary shared by product adapters.
// Products may choose a product-local authenticated prefix for management APIs,
// but the unauthenticated pairing ceremony and signed peer RPC paths are stable.
const (
	JoinPath       = "/api/cluster/v1/join"
	RPCSummaryPath = "/api/cluster/v1/rpc/summary"
	RPCComparePath = "/api/cluster/v1/rpc/compare"
)

var CommonLifecycle = []string{"invite", "join", "approve", "reject", "collect", "rotate", "revoke", "remove"}
var CommonMemberStates = []string{MemberActive, MemberDisabled, MemberRevoked}
var CommonManagementSections = []string{"overview", "pair-node", "pending-invitations", "members", "health", "credential-rotation", "audit-history"}

const (
	AuditInviteCreate  = "cluster.invite.create"
	AuditJoinApprove   = "cluster.join.approve"
	AuditJoinReject    = "cluster.join.reject"
	AuditMemberDisable = "cluster.member.disable"
	AuditMemberEnable  = "cluster.member.enable"
	AuditMemberRotate  = "cluster.member.rotate"
	AuditMemberRevoke  = "cluster.member.revoke"
	AuditMemberRemove  = "cluster.member.remove"
)
