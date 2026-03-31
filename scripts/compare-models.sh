#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<'EOF'
Usage: scripts/compare-models.sh [options]

Compare embedding and reranker model combinations using the real vector-mcp-go
indexing and HTTP search path.

Requirements:
  - bash
  - curl
  - python3
  - Go toolchain

Query file format:
  tab-separated file with:
    query<TAB>expected_path

Example:
  scripts/compare-models.sh \
    --repo /path/to/repo \
    --queries scripts/model-compare-queries.tsv

Options:
  --repo PATH              Repository path to index. Default: current directory
  --queries PATH           TSV file of query and expected relative path pairs
  --top-k N                Search top_k. Default: 5
  --repetitions N          Repetitions per query for latency sampling. Default: 3
  --port-base N            Starting API port for per-run daemons. Default: 47921
  --out-dir PATH           Output directory. Default: /tmp/vector-mcp-model-compare-<ts>
  --embed-models LIST      Comma-separated embeddings to test
  --rerankers LIST         Comma-separated rerankers to test. Use 'none' to disable
  --keep-artifacts         Keep per-run data directories and logs
  --skip-build             Reuse existing /tmp/vector-mcp-go-compare binary
  -h, --help               Show this help

Supported embeddings:
  BAAI/bge-small-en-v1.5
  BAAI/bge-base-en-v1.5
  Xenova/bge-m3

Supported rerankers:
  none
  cross-encoder/ms-marco-MiniLM-L-6-v2
  Xenova/bge-reranker-base
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

sanitize_name() {
  echo "$1" | tr '/,' '__' | tr -cd 'A-Za-z0-9._-'
}

model_filename() {
  case "$1" in
    "BAAI/bge-small-en-v1.5") echo "bge-small-en-v1.5-q4.onnx" ;;
    "BAAI/bge-base-en-v1.5") echo "bge-base-en-v1.5-q4.onnx" ;;
    "Xenova/bge-m3") echo "bge-m3-q4.onnx" ;;
    "cross-encoder/ms-marco-MiniLM-L-6-v2") echo "ms-marco-MiniLM-L-6-v2-q4.onnx" ;;
    "Xenova/bge-reranker-base") echo "bge-reranker-base-q4.onnx" ;;
    "none") echo "" ;;
    *)
      echo "Unsupported model: $1" >&2
      exit 1
      ;;
  esac
}

model_dimension() {
  case "$1" in
    "BAAI/bge-small-en-v1.5") echo "384" ;;
    "BAAI/bge-base-en-v1.5") echo "768" ;;
    "Xenova/bge-m3") echo "1024" ;;
    *) echo "" ;;
  esac
}

wait_for_health() {
  local port="$1"
  local tries=0
  while [[ "$tries" -lt 120 ]]; do
    if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then
      return 0
    fi
    tries=$((tries + 1))
    sleep 0.5
  done
  return 1
}

cleanup_pid() {
  local pid="$1"
  if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
}

bytes_to_mb() {
  python3 - "$1" <<'PY'
import sys
value = int(sys.argv[1])
print(f"{value / (1024 * 1024):.2f}")
PY
}

REPO_PATH="$(pwd)"
QUERY_FILE=""
TOP_K=5
REPETITIONS=3
PORT_BASE=47921
OUT_DIR="/tmp/vector-mcp-model-compare-$(date +%Y%m%d-%H%M%S)"
KEEP_ARTIFACTS=0
SKIP_BUILD=0
EMBED_MODELS="BAAI/bge-small-en-v1.5,BAAI/bge-base-en-v1.5,Xenova/bge-m3"
RERANKERS="none,cross-encoder/ms-marco-MiniLM-L-6-v2,Xenova/bge-reranker-base"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO_PATH="$2"
      shift 2
      ;;
    --queries)
      QUERY_FILE="$2"
      shift 2
      ;;
    --top-k)
      TOP_K="$2"
      shift 2
      ;;
    --repetitions)
      REPETITIONS="$2"
      shift 2
      ;;
    --port-base)
      PORT_BASE="$2"
      shift 2
      ;;
    --out-dir)
      OUT_DIR="$2"
      shift 2
      ;;
    --embed-models)
      EMBED_MODELS="$2"
      shift 2
      ;;
    --rerankers)
      RERANKERS="$2"
      shift 2
      ;;
    --keep-artifacts)
      KEEP_ARTIFACTS=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

require_cmd go
require_cmd curl
require_cmd python3

if [[ ! -d "$REPO_PATH" ]]; then
  echo "Repository path not found: $REPO_PATH" >&2
  exit 1
fi

if [[ -n "$QUERY_FILE" && ! -f "$QUERY_FILE" ]]; then
  echo "Query file not found: $QUERY_FILE" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

BINARY_PATH="/tmp/vector-mcp-go-compare"
if [[ "$SKIP_BUILD" -eq 0 ]]; then
  echo "Building vector-mcp-go binary at $BINARY_PATH"
  (
    cd "$REPO_ROOT"
    env GOCACHE=/tmp/vector-mcp-go-build-cache go build -o "$BINARY_PATH" main.go
  )
fi

MODELS_DIR="$OUT_DIR/shared-models"
mkdir -p "$MODELS_DIR"
RESULTS_TSV="$OUT_DIR/results.tsv"

printf "embedding\treranker\tdim\tindex_seconds\tdb_mb\temb_model_mb\treranker_mb\tavg_ms\tp50_ms\tp95_ms\ttop1_acc\thit_rate_at_k\tmrr\tqueries\trepetitions\n" > "$RESULTS_TSV"

IFS=',' read -r -a EMBEDDING_LIST <<<"$EMBED_MODELS"
IFS=',' read -r -a RERANKER_LIST <<<"$RERANKERS"

combo_index=0
for embedding in "${EMBEDDING_LIST[@]}"; do
  emb_file="$(model_filename "$embedding")"
  emb_dim="$(model_dimension "$embedding")"

  for reranker in "${RERANKER_LIST[@]}"; do
    rerank_file="$(model_filename "$reranker")"
    combo_index=$((combo_index + 1))
    combo_label="$(sanitize_name "${embedding}__${reranker}")"
    run_dir="$OUT_DIR/$combo_label"
    rm -rf "$run_dir"
    data_dir="$run_dir/data"
    db_path="$data_dir/lancedb"
    log_file="$run_dir/server.log"
    metrics_dir="$run_dir/metrics"
    lat_file="$metrics_dir/latencies_ms.txt"
    score_file="$metrics_dir/scores.txt"
    mkdir -p "$metrics_dir"
    : > "$lat_file"
    : > "$score_file"

    port=$((PORT_BASE + combo_index - 1))

    echo
    echo "=== Comparing embedding=$embedding reranker=$reranker on port $port ==="

    index_start_ns="$(date +%s%N)"
    env \
      GOCACHE=/tmp/vector-mcp-go-build-cache \
      PROJECT_ROOT="$REPO_PATH" \
      DATA_DIR="$data_dir" \
      MODELS_DIR="$MODELS_DIR" \
      DB_PATH="$db_path" \
      MODEL_NAME="$embedding" \
      RERANKER_MODEL_NAME="$reranker" \
      DISABLE_FILE_WATCHER=true \
      ENABLE_LIVE_INDEXING=false \
      API_PORT="$port" \
      "$BINARY_PATH" -index
    index_end_ns="$(date +%s%N)"

    index_seconds="$(python3 - "$index_start_ns" "$index_end_ns" <<'PY'
import sys
start_ns = int(sys.argv[1])
end_ns = int(sys.argv[2])
print(f"{(end_ns - start_ns) / 1_000_000_000:.3f}")
PY
)"

    pid=""
    trap 'cleanup_pid "$pid"' EXIT
    env \
      GOCACHE=/tmp/vector-mcp-go-build-cache \
      PROJECT_ROOT="$REPO_PATH" \
      DATA_DIR="$data_dir" \
      MODELS_DIR="$MODELS_DIR" \
      DB_PATH="$db_path" \
      MODEL_NAME="$embedding" \
      RERANKER_MODEL_NAME="$reranker" \
      DISABLE_FILE_WATCHER=true \
      ENABLE_LIVE_INDEXING=false \
      API_PORT="$port" \
      "$BINARY_PATH" -daemon >"$log_file" 2>&1 &
    pid=$!

    if ! wait_for_health "$port"; then
      echo "Server failed to become healthy for combo $combo_label" >&2
      cleanup_pid "$pid"
      trap - EXIT
      exit 1
    fi

    query_count=0
    if [[ -n "$QUERY_FILE" ]]; then
      while IFS=$'\t' read -r query expected_path; do
        if [[ -z "${query}" || "${query:0:1}" == "#" ]]; then
          continue
        fi
        query_count=$((query_count + 1))
        for _ in $(seq 1 "$REPETITIONS"); do
          response_file="$metrics_dir/query_${query_count}.json"
          payload="$(python3 - "$query" "$TOP_K" <<'PY'
import json
import sys
print(json.dumps({"query": sys.argv[1], "top_k": int(sys.argv[2])}))
PY
)"
          latency_seconds="$(
            curl -fsS \
              -H 'Content-Type: application/json' \
              -o "$response_file" \
              -w '%{time_total}' \
              -d "$payload" \
              "http://127.0.0.1:${port}/api/search"
          )"
          python3 - "$response_file" "$expected_path" "$TOP_K" "$latency_seconds" >> "$score_file" <<'PY'
import json
import sys

response_path, expected, top_k, latency_s = sys.argv[1:5]
top_k = int(top_k)
latency_ms = float(latency_s) * 1000.0

with open(response_path, "r", encoding="utf-8") as fh:
    data = json.load(fh)

paths = []
for item in data:
    meta = item.get("metadata") or {}
    path = meta.get("path", "")
    if path and path not in paths:
        paths.append(path)

top_k_paths = paths[:top_k]
top1 = 1.0 if paths[:1] == [expected] else 0.0
hit = 1.0 if expected in top_k_paths else 0.0
mrr = 0.0
for idx, path in enumerate(paths, start=1):
    if path == expected:
        mrr = 1.0 / idx
        break

print(f"{latency_ms:.3f}\t{top1:.6f}\t{hit:.6f}\t{mrr:.6f}")
PY
        done
      done < "$QUERY_FILE"
    fi

    cleanup_pid "$pid"
    trap - EXIT

    db_bytes=0
    if [[ -d "$db_path" ]]; then
      db_bytes="$(du -sb "$db_path" | awk '{print $1}')"
    fi
    emb_bytes=0
    if [[ -n "$emb_file" && -f "$MODELS_DIR/$emb_file" ]]; then
      emb_bytes="$(stat -c %s "$MODELS_DIR/$emb_file")"
    fi
    rerank_bytes=0
    if [[ -n "$rerank_file" && -f "$MODELS_DIR/$rerank_file" ]]; then
      rerank_bytes="$(stat -c %s "$MODELS_DIR/$rerank_file")"
    fi

    db_mb="$(bytes_to_mb "$db_bytes")"
    emb_mb="$(bytes_to_mb "$emb_bytes")"
    rerank_mb="$(bytes_to_mb "$rerank_bytes")"

    if [[ -s "$score_file" ]]; then
      summary="$(python3 - "$score_file" <<'PY'
import math
import sys

path = sys.argv[1]
latencies = []
top1_scores = []
hit_scores = []
mrr_scores = []

with open(path, "r", encoding="utf-8") as fh:
    for line in fh:
        latency, top1, hit, mrr = line.rstrip("\n").split("\t")
        latencies.append(float(latency))
        top1_scores.append(float(top1))
        hit_scores.append(float(hit))
        mrr_scores.append(float(mrr))

latencies.sort()

def percentile(values, p):
    if not values:
        return 0.0
    idx = max(0, math.ceil((p / 100.0) * len(values)) - 1)
    idx = min(idx, len(values) - 1)
    return values[idx]

avg = sum(latencies) / len(latencies)
p50 = percentile(latencies, 50)
p95 = percentile(latencies, 95)
top1 = sum(top1_scores) / len(top1_scores)
hit = sum(hit_scores) / len(hit_scores)
mrr = sum(mrr_scores) / len(mrr_scores)
print(f"{avg:.3f}\t{p50:.3f}\t{p95:.3f}\t{top1:.3f}\t{hit:.3f}\t{mrr:.3f}\t{len(latencies)}")
PY
)"
    else
      summary="0.000	0.000	0.000	0.000	0.000	0.000	0"
    fi

    avg_ms="$(echo "$summary" | awk -F'\t' '{print $1}')"
    p50_ms="$(echo "$summary" | awk -F'\t' '{print $2}')"
    p95_ms="$(echo "$summary" | awk -F'\t' '{print $3}')"
    top1_acc="$(echo "$summary" | awk -F'\t' '{print $4}')"
    hit_rate="$(echo "$summary" | awk -F'\t' '{print $5}')"
    mrr="$(echo "$summary" | awk -F'\t' '{print $6}')"
    samples="$(echo "$summary" | awk -F'\t' '{print $7}')"

    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
      "$embedding" \
      "$reranker" \
      "$emb_dim" \
      "$index_seconds" \
      "$db_mb" \
      "$emb_mb" \
      "$rerank_mb" \
      "$avg_ms" \
      "$p50_ms" \
      "$p95_ms" \
      "$top1_acc" \
      "$hit_rate" \
      "$mrr" \
      "$query_count" \
      "$REPETITIONS" >> "$RESULTS_TSV"

    if [[ "$KEEP_ARTIFACTS" -eq 0 ]]; then
      rm -rf "$run_dir"
    fi
  done
done

echo
echo "Results written to $RESULTS_TSV"
if command -v column >/dev/null 2>&1; then
  column -t -s $'\t' "$RESULTS_TSV"
else
  cat "$RESULTS_TSV"
fi
