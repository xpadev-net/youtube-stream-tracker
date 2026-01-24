package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestRespondOK(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondOK(c, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("RespondOK() status = %v, want %v", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response["status"] != "ok" {
		t.Errorf("RespondOK() response status = %v, want ok", response["status"])
	}
}

func TestRespondCreated(t *testing.T) {
	router := setupRouter()
	router.POST("/test", func(c *gin.Context) {
		RespondCreated(c, gin.H{"id": "123"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("RespondCreated() status = %v, want %v", w.Code, http.StatusCreated)
	}
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response["id"] != "123" {
		t.Errorf("RespondCreated() response id = %v, want 123", response["id"])
	}
}

func TestRespondNoContent(t *testing.T) {
	router := setupRouter()
	router.DELETE("/test", func(c *gin.Context) {
		RespondNoContent(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("RespondNoContent() status = %v, want %v", w.Code, http.StatusNoContent)
	}
	if w.Body.String() != "" {
		t.Errorf("RespondNoContent() body = %v, want empty", w.Body.String())
	}
}

func TestNewErrorResponse(t *testing.T) {
	resp := NewErrorResponse(ErrCodeBadRequest, "Invalid request")

	if resp.Error.Code != ErrCodeBadRequest {
		t.Errorf("NewErrorResponse() code = %v, want %v", resp.Error.Code, ErrCodeBadRequest)
	}
	if resp.Error.Message != "Invalid request" {
		t.Errorf("NewErrorResponse() message = %v, want Invalid request", resp.Error.Message)
	}
}

func TestRespondError(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("RespondError() status = %v, want %v", w.Code, http.StatusBadRequest)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeBadRequest {
		t.Errorf("RespondError() code = %v, want %v", response.Error.Code, ErrCodeBadRequest)
	}
	if response.Error.Message != "Invalid request" {
		t.Errorf("RespondError() message = %v, want Invalid request", response.Error.Message)
	}
}

func TestRespondBadRequest(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondBadRequest(c, "Bad request")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("RespondBadRequest() status = %v, want %v", w.Code, http.StatusBadRequest)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeBadRequest {
		t.Errorf("RespondBadRequest() code = %v, want %v", response.Error.Code, ErrCodeBadRequest)
	}
}

func TestRespondUnauthorized(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondUnauthorized(c, "Unauthorized")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("RespondUnauthorized() status = %v, want %v", w.Code, http.StatusUnauthorized)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeUnauthorized {
		t.Errorf("RespondUnauthorized() code = %v, want %v", response.Error.Code, ErrCodeUnauthorized)
	}
}

func TestRespondNotFound(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondNotFound(c, "Not found")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("RespondNotFound() status = %v, want %v", w.Code, http.StatusNotFound)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeNotFound {
		t.Errorf("RespondNotFound() code = %v, want %v", response.Error.Code, ErrCodeNotFound)
	}
}

func TestRespondConflict(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondConflict(c, ErrCodeDuplicateMonitor, "Duplicate monitor")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("RespondConflict() status = %v, want %v", w.Code, http.StatusConflict)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeDuplicateMonitor {
		t.Errorf("RespondConflict() code = %v, want %v", response.Error.Code, ErrCodeDuplicateMonitor)
	}
}

func TestRespondInternalError(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondInternalError(c, "Internal error")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("RespondInternalError() status = %v, want %v", w.Code, http.StatusInternalServerError)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeInternal {
		t.Errorf("RespondInternalError() code = %v, want %v", response.Error.Code, ErrCodeInternal)
	}
}

func TestRespondValidationError(t *testing.T) {
	router := setupRouter()
	router.GET("/test", func(c *gin.Context) {
		RespondValidationError(c, "Validation error")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("RespondValidationError() status = %v, want %v", w.Code, http.StatusBadRequest)
	}
	var response ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if response.Error.Code != ErrCodeValidation {
		t.Errorf("RespondValidationError() code = %v, want %v", response.Error.Code, ErrCodeValidation)
	}
}
