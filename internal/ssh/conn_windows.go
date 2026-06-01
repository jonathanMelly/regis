// internal/ssh/conn_windows.go
package ssh

import (
	"net"

	"github.com/Microsoft/go-winio"
)

func npipeConn() (net.Conn, error) {
	return winio.DialPipe(`\\.\pipe\openssh-ssh-agent`, nil)
}
