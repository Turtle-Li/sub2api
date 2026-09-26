package basispoints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxToolRepairs = 2

// ToolRepairFunc continues the same BPS conversation after a rejected native
// transport call. The entire tool batch is still unexecuted at this boundary.
type ToolRepairFunc func(context.Context, map[string]any, error) (map[string]any, error)

func (b *Bridge) validateToolResponse(response object) error {
	check := *b
	check.replay = nil
	output, _ := response["output"].([]any)
	ids, calls := make(map[string]bool), make(map[string]bool)
	for _, raw := range output {
		item, _ := raw.(object)
		if !isTool(item) {
			continue
		}
		translated, err := check.translateCall(item)
		if err != nil {
			return err
		}
		id, call := text(translated["id"]), text(translated["call_id"])
		if ids[id] || calls[call] {
			return fmt.Errorf("basispoints response contains duplicate tool identities")
		}
		ids[id], calls[call] = true, true
	}
	return nil
}

// Only complete BPS wrapper batches can be returned as native tool errors.
// Never infer a target for arbitrary code, invent a call ID, or retry I/O.
func repairableTools(response object) ([]object, bool) {
	if status := text(response["status"]); status != "" && status != "completed" {
		return nil, false
	}
	output, _ := response["output"].([]any)
	var items []object
	ids := make(map[string]bool)
	for _, raw := range output {
		item, _ := raw.(object)
		if !isTool(item) {
			continue
		}
		name, call := text(item["name"]), text(item["call_id"])
		if text(item["type"]) != "function_call" || (name != "run_officejs" && name != "functions.run_officejs") || call == "" || ids[call] {
			return nil, false
		}
		args, ok := item["arguments"].(object)
		if !ok && decode([]byte(text(item["arguments"])), &args) != nil {
			return nil, false
		}
		if args == nil {
			return nil, false
		}
		ids[call] = true
		items = append(items, item)
	}
	return items, len(items) > 0 && len(items) <= 1024
}

func (b *Bridge) translateCompleted(ctx context.Context, response object, repair ToolRepairFunc) error {
	validation := b.validateToolResponse(response)
	if validation == nil {
		return b.translateResponse(response)
	}
	original, eligible := repairableTools(response)
	if repair == nil || !eligible {
		return validation
	}
	for _, item := range original {
		if target := transportTarget(transportArguments(item)); target != "" {
			if _, allowed := b.tools[target]; !allowed {
				return validation
			}
		}
	}
	failed := response
	for attempt := 0; attempt < maxToolRepairs; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		corrected, err := repair(ctx, failed, validation)
		if usage, _ := corrected["usage"].(object); usage != nil {
			// 只回报最后一次续轮的用量：它才是会话的真实上下文规模。累加会把上下文算成
			// 数倍，误触发 BPS 上下文上限跳过与客户端压缩；纠正轮的消耗不向客户端计费。
			response["usage"] = usage
		}
		if err != nil {
			return fmt.Errorf("basispoints tool transport correction failed: %w", err)
		}
		items, ok := repairableTools(corrected)
		if !ok || len(items) != len(original) {
			return fmt.Errorf("basispoints tool transport correction changed the tool batch; no tool was executed")
		}
		validation = b.validateToolResponse(corrected)
		if validation == nil {
			items, err = b.restoreRawToolPayloads(original, items)
			if err != nil {
				return err
			}
			if !b.preservesToolOperations(original, items) {
				return fmt.Errorf("basispoints tool transport correction changed an operation; no tool was executed")
			}
			// Text has already streamed. Replace only its withheld tool slots,
			// keeping one downstream response identity and stable output indexes.
			output, _ := response["output"].([]any)
			next := 0
			for i, raw := range output {
				item, _ := raw.(object)
				if isTool(item) {
					output[i] = items[next]
					next++
				}
			}
			return b.translateResponse(response)
		}
		failed = corrected
	}
	return fmt.Errorf("basispoints tool transport remains invalid after %d corrections; no tool was executed: %w", maxToolRepairs, validation)
}

// A correction chooses a declared raw transport, not replacement source text.
// Bind its code field to the original bytes before checking the whole batch.
// Valid operations and named JSON envelopes are never rebound.
// Clone corrected calls so replay records exactly what the client receives,
// without rewriting the model response used by the continuation.
func (b *Bridge) restoreRawToolPayloads(original, corrected []object) ([]object, error) {
	check := *b
	check.replay = nil
	result := append([]object(nil), corrected...)
	for i, native := range original {
		if _, err := check.translateCall(native); err == nil {
			continue
		}
		code, ok := transportArguments(native)["code"].(string)
		if !ok {
			continue
		}
		if envelope, err := decodeTransportEnvelope(code); err == nil {
			if name, nameErr := envelopeName(envelope); nameErr == nil && name != "" {
				continue
			}
		}
		if brokenEnvelope(code) {
			// 残缺的信封不是原始代码，没有可回绑的字节。
			continue
		}
		args := transportArguments(corrected[i])
		summary := text(args["summary"])
		if (!strings.HasPrefix(summary, customTransportPrefix) && !strings.HasPrefix(summary, functionCodeTransportPrefix)) || args["code"] == code {
			continue
		}
		boundArgs := make(object, len(args))
		for key, value := range args {
			boundArgs[key] = value
		}
		boundArgs["code"] = code
		encoded, err := json.Marshal(boundArgs)
		if err != nil {
			return nil, fmt.Errorf("basispoints cannot bind the original tool payload: %w", err)
		}
		bound := make(object, len(corrected[i]))
		for key, value := range corrected[i] {
			bound[key] = value
		}
		bound["arguments"] = string(encoded)
		result[i] = bound
	}
	return result, nil
}

// Valid calls in a mixed batch must retain their exact client operation. For
// unframed raw text, require byte-for-byte preservation in the corrected payload.
// This checks content, without interpreting code or choosing its target.
func (b *Bridge) preservesToolOperations(original, corrected []object) bool {
	check := *b
	check.replay = nil
	for i, native := range original {
		before, err := check.translateCall(native)
		after, afterErr := check.translateCall(corrected[i])
		if afterErr != nil {
			return false
		}
		if err == nil {
			// A call ID changes between model turns; it is not part of the operation.
			before["call_id"], after["call_id"] = "compare", "compare"
			if historyCallFingerprint(before) != historyCallFingerprint(after) {
				return false
			}
			continue
		}
		args := transportArguments(native)
		if target := transportTarget(args); target != "" {
			info, allowed := b.tools[target]
			if !allowed || after["name"] != info.Name || text(after["namespace"]) != info.Namespace {
				return false
			}
		}
		code, isText := args["code"].(string)
		if !isText {
			continue
		}
		if envelope, envelopeErr := decodeTransportEnvelope(code); envelopeErr == nil {
			if name, nameErr := envelopeName(envelope); nameErr == nil && name != "" {
				continue
			}
		}
		if brokenEnvelope(code) {
			// 截断或不合法的 JSON 信封只能由模型整体重发；能认出工具名时要求目标不变。
			if name := b.transportToolHint(code); name != "" && text(after["name"]) != b.tools[name].Name {
				return false
			}
			continue
		}
		if text(after["type"]) == "custom_tool_call" {
			if after["input"] != code {
				return false
			}
		} else {
			var payload object
			// FUNCTION_CODE 按目录声明的字段（code 或 cmd）携带原始文本。
			field := "code"
			if info, ok := b.tools[text(after["name"])]; ok {
				if declared := functionCodeTransportField(text(after["name"]), info.Kind, info.Parameters); declared != "" {
					field = declared
				}
			}
			if decode([]byte(text(after["arguments"])), &payload) != nil || payload[field] != code {
				return false
			}
		}
	}
	return true
}

// brokenEnvelope 判断原始 code 是否是写坏的 JSON 信封（以 { 开头但不是合法 JSON），
// 例如输出被截断。它不是可逐字保留的原始代码。
func brokenEnvelope(code string) bool {
	trimmed := strings.TrimSpace(code)
	return strings.HasPrefix(trimmed, "{") && !json.Valid([]byte(trimmed))
}

func transportArguments(native object) object {
	if args, ok := native["arguments"].(object); ok {
		return args
	}
	var args object
	_ = decode([]byte(text(native["arguments"])), &args)
	return args
}

func transportTarget(args object) string {
	for _, prefix := range []string{customTransportPrefix, functionCodeTransportPrefix} {
		if summary := text(args["summary"]); strings.HasPrefix(summary, prefix) {
			return strings.TrimPrefix(summary, prefix)
		}
	}
	if envelope, err := decodeTransportEnvelope(args["code"]); err == nil {
		name, _ := envelopeName(envelope)
		return name
	}
	return ""
}

// BuildToolRepairRequest extends an already prepared BPS request. It preserves
// the model, effort, scope and native history; no client tool is executed here.
func BuildToolRepairRequest(prepared []byte, failed map[string]any, validation error) ([]byte, error) {
	items, ok := repairableTools(failed)
	if !ok || validation == nil {
		return nil, fmt.Errorf("basispoints tool response is not eligible for correction")
	}
	var request object
	if decode(prepared, &request) != nil || request == nil {
		return nil, fmt.Errorf("invalid prepared Basispoints request")
	}
	input, ok := request["input"].([]any)
	if !ok {
		return nil, fmt.Errorf("basispoints correction requires expanded history")
	}
	output, _ := failed["output"].([]any)
	input = append(input, output...)
	feedback, _ := json.Marshal(object{"executed": false, "error": object{"code": "invalid_client_tool_transport", "message": validation.Error()}})
	for _, item := range items {
		call := text(item["call_id"])
		input = append(input, object{"type": "function_call_output", "id": "fc_" + fingerprint([]any{call, len(input)}), "call_id": call, "output": string(feedback)})
	}
	input = append(input, message("developer", fmt.Sprintf("The preceding tool batch failed transport validation before any client tool was executed. Correct only its transport formatting and return exactly %d run_officejs calls in the same order, preserving the intended operations and exact raw code. Use the existing client catalog: FUNCTION needs one JSON envelope with object arguments; CUSTOM needs summary=codex2api.custom/CATALOG_NAME and raw input in code; FUNCTION_CODE needs its declared marker and metadata JSON in extended_summary. Do not execute Office code, infer an undeclared target, add operations, or repeat commentary.", len(items))))
	request["input"] = input
	metadata, _ := request["metadata"].(object)
	if metadata == nil {
		return nil, fmt.Errorf("basispoints correction requires request metadata")
	}
	iteration, err := strconv.Atoi(text(metadata["agent_iteration"]))
	if err != nil {
		return nil, fmt.Errorf("invalid Basispoints agent iteration")
	}
	metadata["agent_iteration"] = strconv.Itoa(iteration + 1)
	return json.Marshal(request)
}

// ReadToolRepairResponse withholds all intermediate events from the client.
// Only the terminal native response is authoritative, as in the primary stream.
func ReadToolRepairResponse(reader io.Reader) (map[string]any, error) {
	var response object
	var terminalError error
	pending := make(map[string]bool)
	err := readEvents(io.LimitReader(reader, 32<<20), func(event string, data []byte) error {
		if string(data) == "[DONE]" {
			return nil
		}
		var payload object
		if decode(data, &payload) != nil || payload == nil {
			return fmt.Errorf("invalid Basispoints correction SSE event")
		}
		kind := text(payload["type"])
		if kind == "" {
			kind = event
		}
		item, _ := payload["item"].(object)
		if kind == "response.output_item.done" && isTool(item) {
			if len(pending) >= 1024 {
				return fmt.Errorf("too many Basispoints correction tool items")
			}
			pending[text(item["call_id"])+"\x00"+text(item["id"])] = true
		}
		switch kind {
		case "response.completed", "response.failed", "response.incomplete", "error":
			response, _ = payload["response"].(object)
			if kind != "response.completed" || response == nil {
				terminalError = fmt.Errorf("basispoints correction did not complete")
			} else {
				output, _ := response["output"].([]any)
				for _, raw := range output {
					item, _ := raw.(object)
					if isTool(item) {
						delete(pending, text(item["call_id"])+"\x00"+text(item["id"]))
					}
				}
				if len(pending) != 0 {
					terminalError = fmt.Errorf("basispoints correction omitted an original tool item")
				}
			}
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return response, err
	}
	if terminalError != nil {
		return response, terminalError
	}
	if response == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return response, nil
}
