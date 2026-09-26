// Package gen holds the HTTP types and the strict-server interface generated
// from api/arena-api.yaml by oapi-codegen. Regenerate with `go generate ./...`
// (or `make gen`) from anywhere in the module — see api/gen/preprocess.py for
// why generation runs against a derived copy, not the contract itself.
package gen

//go:generate sh -c "cd ../../.. && python3 api/gen/preprocess.py"
//go:generate sh -c "cd ../../.. && go tool oapi-codegen -config api/oapi-codegen-config.yaml api/gen/arena-api.codegen.yaml"
