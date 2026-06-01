// internal/ssh/conn_notwindows.go
//go:build !windows

package ssh

import (
	"errors"
	"net"
)

func npipeConn() (net.Conn, error) {
	return nil, errors.New("named pipes not supported on this platform")
}
