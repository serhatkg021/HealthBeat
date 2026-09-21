package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewServerSetsEveryTimeout(t *testing.T) {
	srv := NewServer(":0", http.NewServeMux(), nil)
	for name, d := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout, "ReadTimeout": srv.ReadTimeout,
		"WriteTimeout": srv.WriteTimeout, "IdleTimeout": srv.IdleTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s is unset: connections can be held open indefinitely", name)
		}
	}
	if srv.ReadHeaderTimeout > 30*time.Second {
		t.Errorf("ReadHeaderTimeout = %v; too lax to stop slow-header attacks", srv.ReadHeaderTimeout)
	}
	if srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 1<<20 {
		t.Errorf("MaxHeaderBytes = %d", srv.MaxHeaderBytes)
	}
}

// Davranış denetimi: eksik bir istek başlığı gönderen karşı taraf, sonsuza dek tutulmak yerine
// yapılandırılan zaman aşımıyla kesilir.
func TestSlowHeaderConnectionIsClosed(t *testing.T) {
	srv := NewServer("", http.NewServeMux(), nil)
	srv.ReadHeaderTimeout = 300 * time.Millisecond // aynı mekanizma, test için kısaltılmış

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET /healthz HTTP/1.1\r\nHost: x\r\n")) // başlık bloğu hiç sonlandırılmadı

	start := time.Now()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = bufio.NewReader(conn).ReadString('\n') // server 408 yanıtlar sonra kapatır ya da doğrudan kapatır
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server still held the half-open connection after %v", time.Since(start))
	}
}
