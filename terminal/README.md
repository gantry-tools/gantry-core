# terminal

`terminal` defines portable terminal-session metadata, validation/defaulting, and byte-oriented bounded scrollback normalization. PTY allocation, WebSocket framing, authentication and database storage remain application responsibilities.

The package is suitable for Warden today and for a future IDE/agent host without importing Warden's privileged server.
