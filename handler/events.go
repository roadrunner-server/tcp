package handler

// These byte slices are treated as read-only sentinel values for worker responses.
// They are package-level vars (not consts) because []byte cannot be const in Go.
var (
	CLOSE      = []byte("CLOSE")      //nolint:gochecknoglobals
	CONTINUE   = []byte("CONTINUE")   //nolint:gochecknoglobals
	WRITECLOSE = []byte("WRITECLOSE") //nolint:gochecknoglobals
	WRITE      = []byte("WRITE")      //nolint:gochecknoglobals
)

type ServerInfo struct {
	RemoteAddr string `json:"remote_addr"`
	Server     string `json:"server"`
	UUID       string `json:"uuid"`
	Event      string `json:"event"`
}

const (
	EventConnected    string = "CONNECTED"
	EventIncomingData string = "DATA"
	EventClose        string = "CLOSE"
)
