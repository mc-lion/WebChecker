package checker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"webchecker/internal/models"
)

const maxBodyBytes = 1 << 20

type Checker struct {
	client *http.Client
}

func New() *Checker {
	return NewWithOptions(false)
}

// NewWithOptions собирает checker; blockPrivateHosts запрещает проверки по
// адресам внутренних сетей. Проверка стоит на уровне соединения, поэтому
// перекрывает и редиректы, и подмену DNS уже после валидации URL.
func NewWithOptions(blockPrivateHosts bool) *Checker {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if blockPrivateHosts {
		dialer.Control = denyPrivateAddress
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 8

	return &Checker{
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after 5 redirects")
				}
				return nil
			},
		},
	}
}

func denyPrivateAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("не удалось разобрать адрес %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("не удалось разобрать IP %q", host)
	}
	if isInternalIP(ip) {
		return fmt.Errorf("адрес %s относится к внутренней сети, проверка запрещена (CHECK_BLOCK_PRIVATE_HOSTS=true)", ip)
	}
	return nil
}

func isInternalIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast()
}

func (c *Checker) Check(ctx context.Context, mon models.Monitor) models.Check {
	result := models.Check{
		MonitorID: mon.ID,
		CheckedAt: time.Now().UTC(),
	}

	timeout := time.Duration(mon.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, mon.URL, nil)
	if err != nil {
		result.ErrorText = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "webChecker/1.0")
	req.Header.Set("Accept", "*/*")

	start := time.Now()
	resp, err := c.client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	}
	result.ResponseMS = int(time.Since(start).Milliseconds())
	if result.ResponseMS < 0 {
		result.ResponseMS = 0
	}
	result.Slow = isSlow(mon.SlowThresholdMS, result.ResponseMS)
	if err != nil {
		result.ErrorText = err.Error()
		return result
	}

	code := resp.StatusCode
	result.StatusCode = &code
	result.OK = code == mon.ExpectedStatus
	if !result.OK {
		result.ErrorText = fmt.Sprintf("unexpected status %d, expected %d", code, mon.ExpectedStatus)
	}
	return result
}

func isSlow(thresholdMS, responseMS int) bool {
	return thresholdMS > 0 && responseMS >= thresholdMS
}
