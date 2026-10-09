//go:build js && wasm

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall/js"

	"github.com/jagarkarlo/nostekon/internal/httpapi"
)

func main() {
	handler := httpapi.NewHandler()
	bridge := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 2 && len(args) != 3 {
			return map[string]any{"status": http.StatusBadRequest, "body": `{"errors":["expected path and body"]}`}
		}
		path := args[0].String()
		method := http.MethodPost
		if path == "/healthz" || path == "/api/v1/schemas/drillrun" || path == "/api/v1/info" {
			method = http.MethodGet
		}
		request := httptest.NewRequest(method, path, strings.NewReader(args[1].String()))
		request.Header.Set("Content-Type", "application/json")
		if len(args) == 3 && args[2].Type() == js.TypeObject {
			for _, name := range []string{"Content-Type", "X-Nostekon-Attestation", "X-Nostekon-Public-Key"} {
				value := args[2].Get(name)
				if value.Type() == js.TypeString {
					request.Header.Set(name, value.String())
				}
			}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return map[string]any{"status": response.Code, "body": response.Body.String()}
	})
	js.Global().Set("nostekonRequest", bridge)
	js.Global().Get("nostekonReady").Invoke()
	select {}
}
