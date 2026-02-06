#!/usr/bin/env bash

set -euo pipefail

REQUEST_FILE=""
TOKEN="${CW_BEARER_TOKEN:-${AWS_Q_BEARER_TOKEN:-}}"
ENDPOINT="https://q.us-east-1.amazonaws.com/generateAssistantResponse"
MAX_TOOLS=80
TIMEOUT=45
KEEP_WORKDIR=false
VERBOSE=false

usage() {
  cat <<'EOF'
CodeWhisperer malformed request 一键诊断脚本

用法:
  scripts/cw_malformed_debug.sh -f request.json -t <bearer_token> [选项]

选项:
  -f, --file <path>         请求 JSON 文件路径（必填）
  -t, --token <token>       Bearer token（可选；默认读取 CW_BEARER_TOKEN/AWS_Q_BEARER_TOKEN）
  -e, --endpoint <url>      请求地址（默认: https://q.us-east-1.amazonaws.com/generateAssistantResponse）
      --max-tools <n>       逐工具探测上限（默认: 80）
      --timeout <sec>       curl 超时秒数（默认: 45）
      --keep-workdir        保留临时目录
  -v, --verbose             输出更详细信息
  -h, --help                显示帮助

示例:
  scripts/cw_malformed_debug.sh -f request.json -t "$CW_TOKEN"
  CW_BEARER_TOKEN=xxx scripts/cw_malformed_debug.sh -f request.json --keep-workdir
EOF
}

log() {
  echo "[diag] $*"
}

err() {
  echo "[diag][error] $*" >&2
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    err "缺少命令: $cmd"
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -f|--file)
      REQUEST_FILE="$2"
      shift 2
      ;;
    -t|--token)
      TOKEN="$2"
      shift 2
      ;;
    -e|--endpoint)
      ENDPOINT="$2"
      shift 2
      ;;
    --max-tools)
      MAX_TOOLS="$2"
      shift 2
      ;;
    --timeout)
      TIMEOUT="$2"
      shift 2
      ;;
    --keep-workdir)
      KEEP_WORKDIR=true
      shift
      ;;
    -v|--verbose)
      VERBOSE=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      err "未知参数: $1"
      usage
      exit 1
      ;;
  esac
done

require_cmd jq
require_cmd curl

if [[ -z "$REQUEST_FILE" ]]; then
  err "缺少 --file"
  usage
  exit 1
fi

if [[ ! -f "$REQUEST_FILE" ]]; then
  err "文件不存在: $REQUEST_FILE"
  exit 1
fi

if [[ -z "$TOKEN" ]]; then
  err "缺少 token，请使用 --token 或环境变量 CW_BEARER_TOKEN/AWS_Q_BEARER_TOKEN"
  exit 1
fi

WORKDIR="$(mktemp -d /tmp/cw-malformed-XXXXXX)"

cleanup() {
  if [[ "$KEEP_WORKDIR" == false ]]; then
    rm -rf "$WORKDIR"
  else
    log "临时目录保留: $WORKDIR"
  fi
}
trap cleanup EXIT

request_code() {
  local payload="$1"
  local tag="$2"
  local headers="$WORKDIR/${tag}.headers"
  local body="$WORKDIR/${tag}.body"
  local code

  code="$(curl -sS -m "$TIMEOUT" -D "$headers" -o "$body" -w '%{http_code}' -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $TOKEN" \
    --data-binary @"$payload" || true)"

  if ! [[ "$code" =~ ^[0-9]{3}$ ]]; then
    code="000"
  fi

  echo "$code"
}

run_variant() {
  local name="$1"
  local file="$2"
  local code
  code="$(request_code "$file" "$name")"
  printf '%-34s %s\n' "$name" "$code"
}

CURRENT_CTX='.conversationState.currentMessage.userInputMessage.userInputMessageContext'

log "工作目录: $WORKDIR"

baseline_code="$(request_code "$REQUEST_FILE" "baseline")"
log "baseline_http_code=$baseline_code"

if [[ "$baseline_code" == "200" ]]; then
  log "请求本身已成功，无需诊断。"
  exit 0
fi

if [[ "$baseline_code" != "400" ]]; then
  log "非 400 响应，仍继续做结构缩小。"
fi

v1="$WORKDIR/v1_no_current_tools.json"
v2="$WORKDIR/v2_current_tools_empty.json"
v3="$WORKDIR/v3_no_current_ctx.json"
v4="$WORKDIR/v4_no_history.json"
v5="$WORKDIR/v5_empty_history_tooluses.json"

jq "del(${CURRENT_CTX}.tools)" "$REQUEST_FILE" > "$v1"
jq "${CURRENT_CTX}.tools=[]" "$REQUEST_FILE" > "$v2"
jq "del(${CURRENT_CTX})" "$REQUEST_FILE" > "$v3"
jq 'del(.conversationState.history)' "$REQUEST_FILE" > "$v4"
jq '.conversationState.history |= map(if has("assistantResponseMessage") then .assistantResponseMessage.toolUses=[] else . end)' "$REQUEST_FILE" > "$v5"

echo
log "阶段1：结构级最小化"
code_v1="$(run_variant v1_no_current_tools "$v1" | awk '{print $NF}')"
code_v2="$(run_variant v2_current_tools_empty "$v2" | awk '{print $NF}')"
code_v3="$(run_variant v3_no_current_ctx "$v3" | awk '{print $NF}')"
code_v4="$(run_variant v4_no_history "$v4" | awk '{print $NF}')"
code_v5="$(run_variant v5_empty_history_tooluses "$v5" | awk '{print $NF}')"

TOOLS_COUNT="$(jq "${CURRENT_CTX}.tools | length // 0" "$REQUEST_FILE")"

echo
log "当前请求 tools 数量: $TOOLS_COUNT"

if [[ "$code_v1" == "200" || "$code_v2" == "200" || "$code_v3" == "200" ]]; then
  log "判定：问题高度集中在 currentMessage.userInputMessage.userInputMessageContext.tools"

  if (( TOOLS_COUNT > 0 )); then
    echo
    log "阶段2：逐工具探测"

    limit="$TOOLS_COUNT"
    if (( limit > MAX_TOOLS )); then
      limit="$MAX_TOOLS"
    fi

    declare -a suspect_idx=()
    declare -a suspect_name=()

    for ((i=0; i<limit; i++)); do
      one_file="$WORKDIR/one_tool_${i}.json"
      jq --argjson idx "$i" "${CURRENT_CTX}.tools = [${CURRENT_CTX}.tools[\$idx]]" "$REQUEST_FILE" > "$one_file"

      one_code="$(request_code "$one_file" "one_tool_${i}")"
      one_name="$(jq -r --argjson idx "$i" "${CURRENT_CTX}.tools[\$idx].toolSpecification.name // \"<unknown>\"" "$REQUEST_FILE")"
      printf '%-4s %-50s %s\n' "[$i]" "$one_name" "$one_code"

      if [[ "$one_code" != "200" ]]; then
        suspect_idx+=("$i")
        suspect_name+=("$one_name")
      fi
    done

    echo
    if (( ${#suspect_idx[@]} == 0 )); then
      log "逐工具未发现单点失败，可能是组合冲突。"
    else
      log "疑似坏工具(${#suspect_idx[@]}个)："
      for ((k=0; k<${#suspect_idx[@]}; k++)); do
        echo "  - [${suspect_idx[$k]}] ${suspect_name[$k]}"
      done
    fi

    echo
    log "阶段3：扫描工具 schema 中的高风险旧字段"
    jq -r '
      .conversationState.currentMessage.userInputMessage.userInputMessageContext.tools // []
      | to_entries[]
      | .key as $idx
      | .value.toolSpecification as $spec
      | ($spec.inputSchema.json // {}) as $schema
      | ($schema | paths(scalars)) as $p
      | select((($schema | getpath($p) | type) == "boolean") and (($p[-1] == "exclusiveMinimum") or ($p[-1] == "exclusiveMaximum")))
      | "[\($idx)] \($spec.name)\t\($p|join("."))\tvalue=\($schema|getpath($p))"
    ' "$REQUEST_FILE" || true

    fixed_bounds="$WORKDIR/fixed_bounds_candidate.json"
    jq '
      def fix_bounds:
        if type == "object" then
          (with_entries(.value |= fix_bounds)
          | if has("exclusiveMinimum") and (.exclusiveMinimum|type)=="boolean" then
              if .exclusiveMinimum == true and has("minimum") and ((.minimum|type)=="number")
                then .exclusiveMinimum=.minimum | del(.minimum)
                else del(.exclusiveMinimum)
              end
            else . end
          | if has("exclusiveMaximum") and (.exclusiveMaximum|type)=="boolean" then
              if .exclusiveMaximum == true and has("maximum") and ((.maximum|type)=="number")
                then .exclusiveMaximum=.maximum | del(.maximum)
                else del(.exclusiveMaximum)
              end
            else . end)
        elif type == "array" then map(fix_bounds)
        else . end;

      .conversationState.currentMessage.userInputMessage.userInputMessageContext.tools |=
      ((. // []) | map(.toolSpecification.inputSchema.json |= fix_bounds))
    ' "$REQUEST_FILE" > "$fixed_bounds"

    fixed_code="$(request_code "$fixed_bounds" "fixed_bounds_candidate")"
    echo
    log "自动修复候选（exclusive* 旧写法）状态码: $fixed_code"
    if [[ "$fixed_code" == "200" ]]; then
      log "结论：高概率是 JSON Schema 旧写法（exclusiveMinimum/exclusiveMaximum 布尔值）导致。"
      log "已生成候选文件: $fixed_bounds"
    fi
  fi
else
  log "current tools 不是唯一主因，建议继续做 history/toolResults 方向诊断。"
fi

echo
log "诊断完成"
log "如需保留所有中间文件，请加 --keep-workdir"

