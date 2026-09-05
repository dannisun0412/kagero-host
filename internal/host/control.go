package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

func Control(dir, method, path string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	reply, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Kagero Host 未运行，请执行 kagero-host setup：%w", err)
	}
	defer reply.Body.Close()
	data, err = io.ReadAll(io.LimitReader(reply.Body, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return fmt.Errorf("控制响应过大")
	}
	if reply.StatusCode != 200 {
		return fmt.Errorf("%s", bytes.TrimSpace(data))
	}
	return json.Unmarshal(data, out)
}
