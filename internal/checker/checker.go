package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"webchecker/internal/models"
)

const maxBodyBytes = 1 << 20

type Checker struct {
	client *http.Client
}

func New() *Checker {
	transport := http.DefaultTransport.(*http.Transport).Clone()
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
	result.ResponseMS = int(time.Since(start).Milliseconds())
	if result.ResponseMS < 0 {
		result.ResponseMS = 0
	}
	if err != nil {
		result.ErrorText = err.Error()
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))

	code := resp.StatusCode
	result.StatusCode = &code
	result.OK = code == mon.ExpectedStatus
	result.Slow = result.ResponseMS > mon.SlowThresholdMS
	if !result.OK {
		result.ErrorText = fmt.Sprintf("unexpected status %d, expected %d", code, mon.ExpectedStatus)
	}
	return result
}
