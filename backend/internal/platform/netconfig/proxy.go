package netconfig

// Proxy contains only the runtime network configuration required by an outgoing client.
type Proxy struct {
	Enabled bool
	URL     string
}
