#!/usr/bin/env bash

set -euo pipefail

# Project root
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY_PATH="/tmp/vector-mcp-go-bench-all"
MODELS_DIR="$REPO_ROOT/models"
DATA_DIR="/tmp/v-mcp-bench-all-data"
PORT=47899

# Cleanup function
cleanup() {
  echo "Stopping background processes..."
  pkill -f "$BINARY_PATH" || true
  rm -rf "$DATA_DIR"
}
trap cleanup EXIT

# 1. Build project
echo "Building project..."
env GOCACHE=/tmp/go-build-cache go build -o "$BINARY_PATH" "$REPO_ROOT/main.go"

# 2. Test combinations
embedders=("BAAI/bge-small-en-v1.5" "Xenova/bge-m3")
rerankers=("none" "cross-encoder/ms-marco-MiniLM-L-6-v2")

# Queries for accuracy and latency
declare -A queries
queries["find user by id"]="benchmark/fixtures/polyglot/service.ts"
queries["add two integers"]="benchmark/fixtures/polyglot/main.go"
queries["normalize string lowercase"]="benchmark/fixtures/polyglot/worker.py"
queries["hybrid search reciprocal rank"]="internal/db/store.go"
queries["load default config"]="internal/config/config.go"
queries["onnx runtime initialization"]="internal/onnx/onnx.go"

printf "\n%-30s | %-12s | %-12s | %-12s | %-12s\n" "Model (Emb + Rerank)" "Index (s)" "Avg Lat (ms)" "P95 Lat (ms)" "Hit Rate @ 5"
printf "%-30s-|-%-12s-|-%-12s-|-%-12s-|-%-12s\n" "------------------------------" "------------" "------------" "------------" "------------"

for embedder in "${embedders[@]}"; do
  for reranker in "${rerankers[@]}"; do
    label="${embedder##*/} + ${reranker##*/}"
    
    # Check if models exist
    emb_file="bge-small-en-v1.5-q4.onnx"
    [[ "$embedder" == "Xenova/bge-m3" ]] && emb_file="bge-m3-q4.onnx"
    
    rerank_file=""
    [[ "$reranker" != "none" ]] && rerank_file="ms-marco-MiniLM-L-6-v2-q4.onnx"
    
    if [[ ! -f "$MODELS_DIR/$emb_file" ]]; then continue; fi
    if [[ -n "$rerank_file" && ! -f "$MODELS_DIR/$rerank_file" ]]; then continue; fi

    rm -rf "$DATA_DIR"
    mkdir -p "$DATA_DIR"

    # Index
    echo -n "Indexing $label... "
    start_index=$(date +%s%N)
    env \
      PROJECT_ROOT="$REPO_ROOT" \
      DATA_DIR="$DATA_DIR" \
      MODELS_DIR="$MODELS_DIR" \
      MODEL_NAME="$embedder" \
      RERANKER_MODEL_NAME="$reranker" \
      DISABLE_FILE_WATCHER=true \
      "$BINARY_PATH" -index > /dev/null 2>&1
    end_index=$(date +%s%N)
    index_sec=$(echo "scale=2; ($end_index - $start_index) / 1000000000" | bc)
    echo "Done ($index_sec s)"

    # Start daemon
    env \
      PROJECT_ROOT="$REPO_ROOT" \
      DATA_DIR="$DATA_DIR" \
      MODELS_DIR="$MODELS_DIR" \
      MODEL_NAME="$embedder" \
      RERANKER_MODEL_NAME="$reranker" \
      DISABLE_FILE_WATCHER=true \
      API_PORT="$PORT" \
      "$BINARY_PATH" -daemon > /dev/null 2>&1 &
    
    daemon_pid=$!
    
    # Wait for health
    for i in {1..40}; do
      if curl -s "http://localhost:$PORT/api/health" > /dev/null; then break; fi
      sleep 0.5
    done

    # Benchmark search
    latencies=()
    hits=0
    count=0
    for q in "${!queries[@]}"; do
      expected="${queries[$q]}"
      count=$((count + 1))
      
      start_q=$(date +%s%N)
      resp=$(curl -s -X POST -H "Content-Type: application/json" \
           -d "{\"query\": \"$q\", \"top_k\": 5}" \
           "http://localhost:$PORT/api/search")
      end_q=$(date +%s%N)
      
      lat_ms=$(echo "scale=2; ($end_q - $start_q) / 1000000" | bc)
      latencies+=("$lat_ms")
      
      # Check if expected path is in the response metadata
      if echo "$resp" | grep -q "\"path\":\"$expected\""; then
        hits=$((hits + 1))
      fi
    done

    # Statistics
    avg_lat=$(printf "%s\n" "${latencies[@]}" | awk '{sum+=$1} END {printf "%.2f", sum/NR}')
    p95_lat=$(printf "%s\n" "${latencies[@]}" | sort -n | awk '{all[NR]=$1} END {idx=int(NR*0.95); if (idx==0) idx=1; printf "%.2f", all[idx]}')
    hit_rate=$(echo "scale=2; $hits / $count" | bc)

    printf "%-30s | %-12s | %-12s | %-12s | %-12s\n" "$label" "$index_sec" "$avg_lat" "$p95_lat" "$hit_rate"

    kill "$daemon_pid" || true
    wait "$daemon_pid" 2>/dev/null || true
  done
done

echo -e "\nFull Benchmark Complete."
