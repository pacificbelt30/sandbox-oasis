package localapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd"
)

type methodSpec struct {
	Name         string
	Params       any
	Result       any
	Mutation     bool
	Retry        string
	Cancellation string
}

func methods() []methodSpec {
	return []methodSpec{
		{"open", OpenParams{}, Capabilities{}, false, "never", "close_handles"},
		{"cancel", CancelParams{}, CancelResult{}, false, "never", "cancel_call_only"},
		{"capabilities", struct{}{}, Capabilities{}, false, "read_only", "cancel_call_only"},
		{"proxy_capabilities", struct{}{}, cxd.ProxyCapabilities{}, false, "read_only", "cancel_call_only"},
		{"profiles", struct{}{}, []cxd.Profile{}, false, "read_only", "cancel_call_only"},
		{"resource_policies", struct{}{}, []cxd.ResourcePolicy{}, false, "read_only", "cancel_call_only"},
		{"resource_usage", WorkerParams{}, cxd.ResourceUsage{}, false, "read_only", "cancel_call_only"},
		{"create", LaunchParams{}, cxd.Worker{}, true, "same_request_id_and_options", "reconcile_request"},
		{"launch", LaunchParams{}, cxd.Worker{}, true, "same_request_id_and_options", "reconcile_request"},
		{"get", WorkerParams{}, cxd.Worker{}, false, "read_only", "cancel_call_only"},
		{"list", struct{}{}, []cxd.Worker{}, false, "read_only", "cancel_call_only"},
		{"request", RequestParams{}, cxd.RequestStatus{}, false, "read_only", "cancel_call_only"},
		{"remove_request", RemoveRequestParams{}, Success{}, true, "same_request_id", "reconcile_request"},
		{"start", WorkerParams{}, Success{}, true, "reconcile_worker", "reconcile_worker"},
		{"stop", StopParams{}, Success{}, true, "reconcile_worker", "reconcile_worker"},
		{"remove", RemoveParams{}, Success{}, true, "reconcile_worker", "reconcile_worker"},
		{"quarantine", WorkerParams{}, Success{}, true, "same_worker_id", "reconcile_worker"},
		{"wait", WorkerParams{}, ExitResult{}, false, "read_only", "cancel_wait_only"},
		{"exec", ExecParams{}, ExecResult{}, true, "never", "stop_guest_command_then_reconcile"},
		{"read_file", FileParams{}, DataResult{}, false, "read_only", "cancel_call_only"},
		{"write_file", FileParams{}, Success{}, true, "reconcile_file", "reconcile_file"},
		{"list_files", FileParams{}, DataResult{}, false, "read_only", "cancel_call_only"},
		{"start_job", ExecParams{}, cxd.Job{}, true, "never", "reconcile_jobs"},
		{"job", JobParams{}, cxd.Job{}, false, "read_only", "cancel_call_only"},
		{"list_jobs", WorkerParams{}, []cxd.Job{}, false, "read_only", "cancel_call_only"},
		{"job_logs", JobParams{}, cxd.JobOutput{}, false, "read_only", "cancel_call_only"},
		{"kill_job", JobParams{}, Success{}, true, "same_job_id", "reconcile_job"},
		{"wait_job", JobParams{}, cxd.Job{}, false, "read_only", "cancel_wait_only"},
		{"checkpoint", CheckpointParams{}, cxd.DiskCheckpoint{}, true, "never", "reconcile_checkpoints"},
		{"checkpoints", struct{}{}, cxd.CheckpointCatalog{}, false, "read_only", "cancel_call_only"},
		{"inspect_checkpoint", CheckpointParams{}, cxd.DiskCheckpoint{}, false, "read_only", "cancel_call_only"},
		{"remove_checkpoint", CheckpointParams{}, Success{}, true, "reconcile_checkpoints", "reconcile_checkpoints"},
	}
}

// Schema generates the reviewable contract from the exact Go DTOs used by the
// adapter. Updating DTOs without regenerating the checked-in schema fails tests.
func Schema() ([]byte, error) {
	g := schemaGenerator{defs: map[string]any{}}
	requests := []any{}
	resultSchemas := []any{}
	responses := []any{}
	metadata := []any{}
	for _, m := range methods() {
		param := g.schema(reflect.TypeOf(m.Params))
		result := g.schema(reflect.TypeOf(m.Result))
		requests = append(requests, map[string]any{"type": "object", "additionalProperties": false, "required": []string{"version", "id", "method", "params"}, "properties": map[string]any{
			"version": map[string]any{"const": ProtocolVersion}, "id": map[string]any{"type": "integer", "minimum": 1, "maximum": 1<<53 - 1}, "method": map[string]any{"const": m.Name}, "params": param, "timeout_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": MaxTimeoutMS, "default": 180000},
		}})
		resultSchemas = append(resultSchemas, result)
		responses = append(responses, map[string]any{"method": m.Name, "result": result})
		metadata = append(metadata, map[string]any{"name": m.Name, "mutation": m.Mutation, "retry": m.Retry, "cancel": m.Cancellation})
	}
	g.schema(reflect.TypeFor[Error]())
	errorSchema, ok := g.defs["APIError"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing generated error schema")
	}
	errorDef, ok := errorSchema["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing generated error properties")
	}
	errorCodes := []string{"invalid_argument", "version_mismatch", "conflict", "cleanup_pending", "outcome_unknown", "request_gone", "in_progress", "capacity", "not_found", "canceled", "deadline_exceeded", "permission_denied", "output_limit", "not_initialized", "busy", "internal"}
	errorDef["code"] = map[string]any{"type": "string", "enum": errorCodes}
	errorDef["codes"] = map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": errorCodes}}
	if launch, ok := g.defs["LaunchRequest"].(map[string]any); ok {
		if props, ok := launch["properties"].(map[string]any); ok {
			props["RequestID"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "x-max-utf8-bytes": 256}
			props["ProjectDir"] = map[string]any{"type": "string", "pattern": "^/"}
		}
	}
	if state, ok := g.defs["RequestStatus"].(map[string]any); ok {
		if props, ok := state["properties"].(map[string]any); ok {
			props["State"] = map[string]any{"type": "string", "enum": []string{"pending", "complete", "failed", "cleanup-pending"}}
		}
	}
	g.defs["Request"] = map[string]any{"oneOf": requests}
	g.defs["Response"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"version", "id"}, "properties": map[string]any{"version": map[string]any{"const": ProtocolVersion}, "id": map[string]any{"type": "integer", "minimum": 0, "maximum": 1<<53 - 1}, "result": map[string]any{"anyOf": resultSchemas}, "error": g.schema(reflect.TypeFor[Error]())}, "anyOf": []any{map[string]any{"required": []string{"result"}}, map[string]any{"required": []string{"error"}}}}
	root := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "https://pacificbelt30.github.io/sandbox-oasis/api/local-v1/schema.json", "title": "Oaiso local embedding protocol v1", "oneOf": []any{map[string]any{"$ref": "#/$defs/Request"}, map[string]any{"$ref": "#/$defs/Response"}}, "$defs": g.defs,
		"x-oaiso": map[string]any{"transport": "ndjson-stdio", "request_ids": "strictly_increasing_from_1_per_process", "launch_ids": "node_wide_durable_RequestID_1_to_256_UTF8_bytes", "automatic_retries": false, "errors": errorCodes, "node_authority": "caller_unix_socket_permissions_root_equivalent", "request_states": []string{"pending", "complete", "failed", "cleanup-pending"}, "worker_status": "opaque_observed_string_never_assume_unknown_means_stopped", "disconnect": "cancel_active_calls_close_handles_workers_may_remain", "unknown_mutation_outcome": "reconcile_request_or_worker_before_retry", "default_general_egress": "deny", "duration_unit": "nanoseconds_except_timeout_ms_and_timeout_seconds", "byte_encoding": "base64", "max_request_bytes": MaxRequestBytes, "max_response_bytes": MaxResponseBytes, "max_output_bytes_per_stream": MaxOutputBytes, "max_concurrent_calls": MaxConcurrentCalls, "methods": metadata, "results": responses},
	}
	data, err := json.MarshalIndent(root, "", "  ")
	return append(data, '\n'), err
}

type schemaGenerator struct{ defs map[string]any }

func (g *schemaGenerator) schema(t reflect.Type) any {
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{g.schema(t.Elem()), map[string]any{"type": "null"}}}
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeFor[time.Duration]() {
		return map[string]any{"type": "integer", "description": "Duration in nanoseconds; use an exactly representable integer."}
	}
	switch t.Kind() {
	case reflect.Struct:
		name := t.Name()
		if t.PkgPath() == "github.com/pacificbelt30/codex-dock/internal/diskcheckpoint" {
			switch name {
			case "Options":
				name = "CheckpointOptions"
			case "Info":
				name = "DiskCheckpoint"
			case "Catalog":
				name = "CheckpointCatalog"
			}
		}
		if t.PkgPath() == "github.com/pacificbelt30/codex-dock/pkg/cxd/localapi" {
			name = "API" + name
		}
		if name == "" {
			return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		}
		if _, ok := g.defs[name]; ok {
			return map[string]any{"$ref": "#/$defs/" + name}
		}
		g.defs[name] = nil
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			key := tag[0]
			if key == "-" {
				continue
			}
			if key == "" {
				key = f.Name
			}
			properties[key] = g.schema(f.Type)
			// DTOs used as input accept Go zero defaults. Responses and resource
			// policies keep their exact fields, including nullable slices/times.
			input := slices.Contains([]string{"Options", "LaunchRequest", "Filesystem", "Lifecycle", "Environment", "Compatibility", "Mount", "EgressPolicy", "Identity", "ModelPolicy", "UsagePolicy", "UsageLimits", "TemporaryStorage", "Limits", "CheckpointOptions"}, name)
			optional := input || slices.Contains(tag, "omitempty") || slices.Contains(tag, "omitzero")
			if name == "LaunchRequest" && (key == "RequestID" || key == "ProjectDir") {
				optional = false
			}
			if name == "APIExecParams" {
				optional = slices.Contains(tag, "omitempty")
			}
			if !optional {
				required = append(required, key)
			}
		}
		g.defs[name] = map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
		return map[string]any{"$ref": "#/$defs/" + name}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": []string{"string", "null"}, "contentEncoding": "base64"}
		}
		return map[string]any{"type": []string{"array", "null"}, "items": g.schema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": g.schema(t.Elem())}
	default:
		panic("unsupported local API schema type: " + t.String())
	}
}
