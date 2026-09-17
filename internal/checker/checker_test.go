package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"webchecker/internal/models"
)

func TestCheckExpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)

	res := New().Check(context.Background(), models.Monitor{
		ID:              1,
		URL:             ts.URL,
		ExpectedStatus:  200,
		TimeoutSeconds:  2,
		SlowThresholdMS: 5000,
	})
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.Slow {
		t.Fatalf("did not expect slow, got %+v", res)
	}
	if res.StatusCode == nil || *res.StatusCode != 200 {
		t.Fatalf("expected status 200, got %+v", res.StatusCode)
	}
}

func TestCheckUnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	res := New().Check(context.Background(), models.Monitor{
		URL:             ts.URL,
		ExpectedStatus:  200,
		TimeoutSeconds:  2,
		SlowThresholdMS: 5000,
	})
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ErrorText == "" {
		t.Fatal("expected error text")
	}
}

func TestCheckSlow(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	res := New().Check(context.Background(), models.Monitor{
		URL:             ts.URL,
		ExpectedStatus:  200,
		TimeoutSeconds:  2,
		SlowThresholdMS: 20,
	})
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	if !res.Slow {
		t.Fatalf("expected slow, got %+v", res)
	}
}

func TestIsSlowThresholdInclusive(t *testing.T) {
	if !isSlow(200, 200) {
		t.Fatal("response equal to threshold should be slow")
	}
	if isSlow(200, 199) {
		t.Fatal("response below threshold should not be slow")
	}
	if isSlow(0, 500) {
		t.Fatal("zero threshold should not mark slow")
	}
}

func TestCheckTimeoutMarkedSlow(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	res := New().Check(context.Background(), models.Monitor{
		URL:             ts.URL,
		ExpectedStatus:  200,
		TimeoutSeconds:  1,
		SlowThresholdMS: 200,
	})
	if res.OK {
		t.Fatal("expected timeout failure")
	}
	if !res.Slow {
		t.Fatalf("timeout longer than slow threshold should be slow, got %+v", res)
	}
}
