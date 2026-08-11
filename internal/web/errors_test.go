package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/testutil"
)

func TestWriteAPIError_UsesStructuredFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeAPIError(rec, &apiError{
		Status:  http.StatusConflict,
		Message: "provider name already exists",
		Reason:  "duplicate_provider",
		Err:     errors.New("duplicate"),
	})

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}

	got := testutil.DecodeJSONMap(t, rec.Body.Bytes())
	if got["error"] != "provider name already exists" {
		t.Fatalf("error = %v", got["error"])
	}
	if got["status"] != float64(http.StatusConflict) {
		t.Fatalf("status field = %v", got["status"])
	}
	if got["reason"] != "duplicate_provider" {
		t.Fatalf("reason = %v", got["reason"])
	}
}

func TestWriteAPIError_FallbacksAndWrapperShape(t *testing.T) {
	t.Parallel()

	t.Run("plain error defaults to 500", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeAPIError(rec, errors.New("boom"))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q", got)
		}

		got := testutil.DecodeJSONMap(t, rec.Body.Bytes())
		if got["error"] != "boom" || got["status"] != float64(http.StatusInternalServerError) || got["reason"] != "" {
			t.Fatalf("body = %#v", got)
		}
	})

	t.Run("api error missing fields falls back", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeAPIError(rec, &apiError{})

		got := testutil.DecodeJSONMap(t, rec.Body.Bytes())
		if got["error"] != "internal server error" {
			t.Fatalf("error = %#v", got)
		}
		if got["status"] != float64(http.StatusInternalServerError) {
			t.Fatalf("status = %#v", got)
		}
		if got["reason"] != "" {
			t.Fatalf("reason = %#v", got)
		}
	})

	t.Run("writeError wrapper keeps stable shape", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, "bad request", http.StatusBadRequest)

		got := testutil.DecodeJSONMap(t, rec.Body.Bytes())
		if got["error"] != "bad request" {
			t.Fatalf("error = %#v", got)
		}
		if got["status"] != float64(http.StatusBadRequest) {
			t.Fatalf("status = %#v", got)
		}
		if _, ok := got["reason"]; !ok {
			t.Fatalf("expected reason key in %#v", got)
		}
	})
}
