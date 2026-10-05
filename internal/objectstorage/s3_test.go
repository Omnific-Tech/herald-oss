package objectstorage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/objectstorage"
)

func TestReadS3CompatibleObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/herald-app/firebase.json" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Error("request was not signed")
		}
		_, _ = response.Write([]byte(`{"type":"service_account"}`))
	}))
	defer server.Close()

	body, err := objectstorage.Read(context.Background(), config.ObjectStorage{
		Provider: "s3", Endpoint: server.URL, Bucket: "herald-app", Region: "test-1",
		AccessKeyID: "access", SecretAccessKey: "secret",
	}, "firebase.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"type":"service_account"}` {
		t.Fatalf("body: %s", body)
	}
}

func TestReadRejectsIncompleteConfiguration(t *testing.T) {
	_, err := objectstorage.Read(context.Background(), config.ObjectStorage{Provider: "s3"}, "firebase.json")
	if err == nil {
		t.Fatal("expected incomplete configuration to fail")
	}
}
