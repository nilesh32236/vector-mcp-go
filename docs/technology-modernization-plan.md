# Technology Verification and Modernization Plan

## 1) Current-State Verification (as of March 31, 2026)

This repository is already using a modern and practical baseline for a deterministic MCP server:

- **Deterministic architecture** (no generative model dependency in server runtime).
- **Fat-tool MCP design** with a narrow tool surface (`search_workspace`, `lsp_query`, `analyze_code`, `workspace_manager`, `modify_workspace`).
- **Local embedding stack** using ONNX runtime (good for privacy, latency, and predictable cost).
- **Go monolith with modular internals** (indexing, embedding, analysis, mutation safety, daemon/watcher).
- **Automated test coverage present** across core packages.

Conclusion: The project is **technically viable** and is a good fit for teams that want local-first, deterministic context retrieval and safe code operations.

---

## 2) Can we implement “latest technology” here?

**Yes — with constraints.**

You can adopt modern capabilities **without violating determinism** by focusing on:

1. **Retrieval quality upgrades** (better reranking, chunking quality, metadata scoring, hybrid ranking).
2. **Performance upgrades** (incremental indexing, adaptive caching, streaming partial results).
3. **Reliability and safety upgrades** (strong parameter clamping, UTF-8 rune-safe truncation everywhere, fuzz/property tests).
4. **Operations upgrades** (metrics, traces, benchmark gates, release automation).

You should avoid adding server-side generative behavior; this repository is designed so the client LLM handles reasoning.

---

## 3) Advantages vs Disadvantages

## Advantages

- **Deterministic and auditable outputs**: easier debugging and safer enterprise adoption.
- **Data privacy**: local embeddings and local vector storage reduce third-party exposure.
- **Lower variable cost**: less dependency on paid external inference APIs.
- **Strong MCP usability**: fat-tool pattern reduces tool confusion for agents.
- **Good extensibility path**: actions can be added into existing unified tools.

## Disadvantages / Trade-offs

- **Quality ceiling from non-generative server design**: no server-side synthesis means quality depends on retrieval and client prompting.
- **ONNX/CGO operational complexity**: native runtime handling can complicate distribution and CI.
- **Index freshness pressure**: large repos need careful incremental indexing and backpressure control.
- **Potential latency variance** on big workspaces if ranking/index operations are not tuned.
- **Higher engineering burden** for deterministic heuristics that approximate what gen models might summarize quickly.

---

## 4) Fit Assessment

This project is a **strong fit** if your goals are:

- deterministic MCP tool execution,
- local/private semantic search,
- safe workspace mutation workflows,
- and stable enterprise behavior over “creative” server outputs.

It is a **weaker fit** if your primary goal is autonomous generative coding inside the server itself.

---

## 5) Recommended Implementation Plan

## Phase 0 — Baseline & Guardrails (1 week)

- Freeze a benchmark dataset of representative repositories.
- Define quality KPIs:
  - retrieval recall@k,
  - MRR/NDCG for top results,
  - index time per KLOC,
  - p50/p95 tool latency.
- Enforce global guardrails:
  - numeric parameter clamp helpers (`topK`, `limit`, etc.),
  - rune-safe truncation utility for all output caps,
  - timeout/cancellation defaults for expensive paths.

**Exit criteria:** reproducible baseline report committed.

## Phase 1 — Retrieval Quality Upgrade (2–3 weeks)

- Improve chunking policy by language/file type.
- Add richer ranking features (symbol hits, proximity, path relevance, recency weighting).
- Strengthen rerank pipeline with deterministic score blending.
- Expand test fixtures for polyglot repositories.

**Integrate through existing fat tools only** (primarily `search_workspace` and `analyze_code` actions).

**Exit criteria:** measurable recall/MRR uplift without p95 regression >15%.

## Phase 2 — Performance & Scale (2 weeks)

- Introduce more aggressive incremental indexing and dirty-file scheduling.
- Add memory-aware backpressure tuning in worker/index pipeline.
- Optional parallel embedding batch optimization with bounded concurrency.

**Exit criteria:** index throughput and p95 latency improvements on large repos.

## Phase 3 — Reliability & Safety Hardening (1–2 weeks)

- Add fuzz tests for request payloads and edge-case encodings.
- Add integration tests for daemon master/slave failover behavior.
- Add panic-proofing around all slices/limits from user input.

**Exit criteria:** zero known crashers in fuzz and integration suites.

## Phase 4 — Operational Excellence (1 week)

- Add OpenTelemetry metrics/tracing hooks (deterministic telemetry only).
- Publish dashboard templates for latency/index health.
- Add release pipeline checks (benchmarks + tests + lint gates).

**Exit criteria:** release checklist fully automated.

---

## 6) Decision Matrix (Go / No-Go)

Proceed now if all are true:

- You want deterministic, local-first architecture.
- You can invest in retrieval/index engineering rather than server-side generation.
- You need predictable cost and stronger privacy posture.

Defer or redesign if:

- You need server to generate long-form reasoning itself,
- or you cannot support native runtime dependencies in your deployment environment.

---

## 7) Practical Next Steps (Immediate)

1. Create a `benchmark/` fixture set and KPI script.
2. Add shared helpers for parameter clamping + rune-safe truncation.
3. Add `search_workspace` ranking action improvements behind a config flag.
4. Run A/B benchmark and promote only if KPI gates pass.

