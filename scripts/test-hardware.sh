#!/usr/bin/env bash

set -euo pipefail

# Project root
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY_PATH="/tmp/vector-mcp-go-bench"
MODELS_DIR="$REPO_ROOT/models"
DATA_DIR="/tmp/v-mcp-bench-data"
PORT=47999

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

# 2. Test models
models=("BAAI/bge-small-en-v1.5" "Xenova/bge-m3")

printf "\n%-25s | %-12s | %-12s | %-12s\n" "Model" "Index (s)" "Avg Lat (ms)" "P95 Lat (ms)"
printf "%-25s-|-%-12s-|-%-12s-|-%-12s\n" "-------------------------" "------------" "------------" "------------"

for model in "${models[@]}"; do
  # Check if model exists locally
  # BGE-Small: bge-small-en-v1.5-q4.onnx
  # BGE-M3: bge-m3-q4.onnx
  filename="bge-small-en-v1.5-q4.onnx"
  if [[ "$model" == "Xenova/bge-m3" ]]; then
    filename="bge-m3-q4.onnx"
  fi

  if [[ ! -f "$MODELS_DIR/$filename" ]]; then
    echo "Skipping $model (not found in $MODELS_DIR)"
    continue
  fi

  rm -rf "$DATA_DIR"
  mkdir -p "$DATA_DIR"

  # Index the current repo, excluding large/binary dirs
  # Note: The app's indexer automatically excludes some, but we'll be safe
  echo "Indexing $model..."
  start_index=$(date +%s%N)
  
  env \
    PROJECT_ROOT="$REPO_ROOT" \
    DATA_DIR="$DATA_DIR" \
    MODELS_DIR="$MODELS_DIR" \
    MODEL_NAME="$model" \
    RERANKER_MODEL_NAME="none" \
    DISABLE_FILE_WATCHER=true \
    "$BINARY_PATH" -index > /dev/null 2>&1
  
  end_index=$(date +%s%N)
  index_sec=$(echo "scale=3; ($end_index - $start_index) / 1000000000" | bc)

  echo "Starting daemon for $model..."
  env \
    PROJECT_ROOT="$REPO_ROOT" \
    DATA_DIR="$DATA_DIR" \
    MODELS_DIR="$MODELS_DIR" \
    MODEL_NAME="$model" \
    RERANKER_MODEL_NAME="none" \
    DISABLE_FILE_WATCHER=true \
    API_PORT="$PORT" \
    "$BINARY_PATH" -daemon > /dev/null 2>&1 &
  
  daemon_pid=$!
  
  # Wait for health
  echo "Waiting for health check..."
  for i in {1..40}; do
    if curl -s "http://localhost:$PORT/api/health" > /dev/null; then
      break
    fi
    sleep 0.5
  done

  # Benchmark search latency (20 queries)
  echo "Running 20 search queries..."
  for i in {1..20}; do
    query="how to index a project"
    if (( i % 2 == 0 )); then query="onnx runtime initialization"; fi
    if (( i % 3 == 0 )); then query="db connect function"; fi
    
    start_q=$(date +%s%N)
    curl -s -X POST -H "Content-Type: application/json" \
         -d "{\"query\": \"$query\", \"top_k\": 5}" \
         "http://localhost:$PORT/api/search" > /dev/null
    end_q=$(date +%s%N)
    
    lat_ms=$(echo "scale=3; ($end_q - $start_q) / 1000000" | bc)
    latencies+=("$lat_ms")
  done

  # Calculate Avg and P95
  avg_lat=$(printf "%s\n" "${latencies[@]}" | awk '{sum+=$1} END {printf "%.2f", sum/NR}')
  p95_lat=$(printf "%s\n" "${latencies[@]}" | sort -n | awk '{all[NR]=$1} END {idx=int(NR*0.95); if (idx==0) idx=1; printf "%.2f", all[idx]}')

  printf "%-25s | %-12s | %-12s | %-12s\n" "$model" "$index_sec" "$avg_lat" "$p95_lat"

  kill "$daemon_pid" || true
  wait "$daemon_pid" 2>/dev/null || true
done

echo -e "\nBenchmarking Complete."
