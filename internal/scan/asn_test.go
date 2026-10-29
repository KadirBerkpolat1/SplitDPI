package scan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

func TestLookupASNOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"org":"AS9121 Turk Telekom","country":"TR"}`))
	}))
	defer srv.Close()

	info, err := scan.LookupASN(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.ASN != 9121 || info.Country != "TR" || info.Org != "Turk Telekom" {
		t.Fatalf("got %+v", info)
	}
}

func TestLookupASNMissingOrg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"country":"TR"}`))
	}))
	defer srv.Close()
	_, err := scan.LookupASN(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLookupASN429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := scan.LookupASN(context.Background(), srv.Client(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("got %v", err)
	}
}
