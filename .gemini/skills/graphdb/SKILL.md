---
name: graphdb
description: Expert in analyzing project architecture using a Neo4j Code Property Graph (CPG) enhanced with Vector Search. Answers questions about dependencies, seams, testing contexts, implicit links, and risks.
---

# Graph Database Skill (Go-Powered)

You are an expert in analyzing the project's architecture using a high-performance Code Property Graph (CPG) built with Go and Neo4j.
Your goal is to answer questions about dependencies, seams, testing contexts, and architectural risks using both structural analysis and the RPG (Repository Planning Graph) Intent Layer.

## Tool Usage
You will use the `graphdb` Go binary directly. Always execute commands from the **project root directory**.

**Binary Location Selection:**
*   **Linux / macOS / WSL:** `.gemini/skills/graphdb/scripts/graphdb`
*   **Windows (PowerShell / CMD):** `.gemini/skills/graphdb/scripts/graphdb-win.exe`

**Variable Definition:**
Define `${graphdb_bin}` as the path to the binary appropriate for the current operating system. Note: If you are running in WSL (Windows Subsystem for Linux), you are in a Linux environment and MUST use the Linux binary.

**Base Command:** `${graphdb_bin} <command> [options]`

## Setup & Infrastructure

### Installation
The skill relies on a pre-compiled Go binary (`${graphdb_bin}`).
If it does not exist, build it from the project root: `make build` (Linux/macOS) or use the cross-compilation script for Windows.

### Environment Variables
The tool automatically inherits the following environment variables. Assume they are already configured correctly via the `.env` file or host system. 
*   `NEO4J_URI`, `NEO4J_USER`, `NEO4J_PASSWORD` (Required for `import` and `query`)
*   `GOOGLE_CLOUD_PROJECT` (Required for Vertex AI embeddings)
*   `GOOGLE_CLOUD_LOCATION` (Default: `global`)

## Operational Guidelines for Agents

### Handling Long-Running Commands (`build-all`, `enrich-features`)
When running `build-all` or `enrich-features` on exceptionally large repositories (e.g., 30k+ files), the process can take many minutes (especially the LLM embedding and clustering phases).
* **Inline Execution (Required)**: You MUST **NOT** use `run_shell_command` with `is_background: true` when invoking `build-all`. The tool is specifically designed to show progress output without flooding the context window.
* **Optimized Output**: The GraphDB CLI has "smart TTY detection". When you execute it via the `run_shell_command` tool, it detects it is running headlessly and disables aggressive progress bar updates, only emitting logs at 10% increments. This drastically reduces verbosity and prevents you from hitting output truncation limits. You can safely run these commands inline and wait for them to finish.
* **No Redirection**: Do not redirect output to log files for monitoring; the inline logs are the intended way to track progress while keeping the context lean.

### Credentials
**CRITICAL RULES FOR CREDENTIALS:**
1. You must **NEVER** explicitly set, export, or pass environment variables (like `NEO4J_PASSWORD=...`) in your bash commands. 
2. You must rely purely on the Go binary's internal `.env` loading. 
3. If a command fails due to an `Unauthorized` or authentication error, **STOP**. Do not try to guess or brute-force the password. Report the failure directly to the user and state that the credentials in their environment or `.env` file appear to be invalid or missing.

### Model & Location Errors
**CRITICAL RULES FOR MODEL ERRORS:**
If a command (especially `enrich-features`, `build-all`, or any semantic query) fails with a **404 error** related to the Vertex AI model or location:
1. **STOP IMMEDIATELY**. Do not attempt to retry or automatically switch models/regions.
2. **INTERVENTION REQUIRED**: This indicates that either the `GOOGLE_CLOUD_LOCATION` or the `GEMINI_EMBEDDING_MODEL` (or related model settings) in the `.env` file is mistyped or unsupported in that region.
3. Report the exact error to the user and state that they must verify and correct their `.env` configuration before you can proceed.

## Workflows

### 1. The "One-Shot" Build (Recommended)
To rebuild the entire graph from scratch (Ingest -> Import -> All Enrichment Phases), use the `build-all` command. This single command handles all 6 steps sequentially, building structural nodes, semantic embeddings, contamination models, git history analysis, and test linkages.
```bash
${graphdb_bin} build-all -dir .
```
### 2. Manual Pipeline
If you need granular control over each step (or if a specific enrichment step fails and needs to be re-run), follow this sequence. Note that `build-all` automatically runs all 6 of these steps.

**Step 0: Check Sync Status**
1. Get local commit: `git rev-parse HEAD`
2. Get graph commit: `${graphdb_bin} query -type status`
3. **Decision:** If the commit hashes match, you can **skip** the ingestion pipeline and proceed directly to "Analysis & Querying".

**Step 1: Ingest (Parse & Generate Graph):**
Scans code and generates structural graph JSONL files. We output nodes and edges separately to avoid double-parsing penalties during the import phase.
```bash
${graphdb_bin} ingest -dir . -nodes nodes.jsonl -edges edges.jsonl
```
*   *Options:* 
    *   `-workers` (concurrency)
    *   `-file-list` (specific files)
    *   `-since-commit <hash>`: **Incremental Ingestion.** Only parses files changed since the specified commit and writes directly to Neo4j, skipping JSONL files. Auto-detects if omitted and the graph has a stored state.

**Step 2: Import (Load Structural Graph to Neo4j):**
Loads the structural graph into the active Neo4j database. This must be done **before** enrichment in the new streaming pipeline. Using separate nodes and edges files prevents a massive CPU penalty from scanning a combined file multiple times.
```bash
${graphdb_bin} import -nodes nodes.jsonl -edges edges.jsonl
```
*   *Options:* `-batch-size`.

**Step 3: Enrich (Build Intent Layer - In-Database):**
Performs **Global Semantic Clustering** directly against the live database. Identifies latent functional domains, extracts features, and generates summaries. Memory usage is bounded by batch sizes.
```bash
${graphdb_bin} enrich-features -dir .
```
*   *Options:*
    *   `-batch-size`: Number of nodes to process per LLM/Batch request (default: 20).
    *   `-embed-batch-size`: Batch size for embedding generation (default: 100).

**Step 4: Enrich Contamination (Legacy Modernization Analysis):**
Identifies architectural volatility (e.g., 3rd-party libraries, external namespaces) and propagates it upwards through the call graph. This is essential for finding extraction boundaries, pinch points, and calculating risk scores.
```bash
${graphdb_bin} enrich-contamination
```

**Step 5: Enrich History (Git Integration):**
Analyzes the git commit history to determine file change frequencies and co-change dependencies. This populates data for the `hotspots` query.
```bash
${graphdb_bin} enrich-history -dir . -since "1 year ago"
```
*   *Options:*
    *   `-since`: How far back to analyze history (default: "1 year ago").

**Step 6: Enrich Tests (Link Tests to Production Code):**
Analyzes naming conventions and call patterns to explicitly link test functions to the production code they exercise. This enables the `coverage` query for pinpointing test contexts.
```bash
${graphdb_bin} enrich-tests
```

### 3. Analysis & Querying
The primary way to interact with the graph is via the `query` command.

**Base Syntax:**
```bash
${graphdb_bin} query -type <type> -target "<search_term>" [options]
```

**Global Query Options:**
*   `-dir <path>`: Base directory for source files (useful for `fetch-source` if querying from outside the project root).
*   `-limit <int>`: Restricts the number of results returned (Default: 10). Primarily affects semantic searches (`search-features`, `search-similar`, `hybrid-context`) and dependency bounds (`neighbors`).
*   `-summary`: (Boolean) If provided, simplifies the JSON output to only include structural metrics/counts instead of full arrays of nodes. Excellent for preventing context-window bloat when querying nodes with massive fan-out (e.g., getting counts of dependencies rather than the full list).

#### Supported Languages & FQN Formats
Structural queries utilize "Fully Qualified Names" (FQN). While the internal database IDs are more complex (including labels and signatures), the query engine is polymorphic and accepts simple FQNs or exact IDs.

*   **C# / .NET / VB.NET:** `Namespace.Class.Method` (No file path)
*   **Java:** `Package.Class.Method` (No file path)
*   **C / C++:** `FilePath:Namespace::Class::Method` (or `FilePath:Class::Method`)
*   **TypeScript:** `FilePath:Class.Method` or `FilePath:Function` (e.g., `src/app.ts:MyClass.myMethod`)
*   **SQL:** `FilePath:Schema.ObjectName` or `FilePath:ObjectName`

#### Query Types Reference

| Type | Description | Target | Options |
| :--- | :--- | :--- | :--- |
| `search-features` | **Intent Search.** Find features/concepts using vector search. | Natural language query | `-limit` |
| `search-similar` | **Code Search.** Find functions semantically similar to a query. | Natural language or code snippet | `-limit` |
| `duplicates` | **Code Search.** Find pairs of duplicated functions using vector embeddings globally. | (Ignored) | `-limit`, `-similarity` (default 0.5) |
| `neighbors` / `test-context` | **Dependency Analysis.** Find immediate callers and callees. | Function Name (exact) | `-depth` |
| `coverage` | **Test Analysis.** Returns tests that cover a specific production function or method. | Function Name/ID | |
| `hybrid-context` | **Combined.** Structural neighbors + semantic similarities. Great for refactoring. | Function Name | `-depth`, `-limit` |
| `impact` | **Risk Analysis.** What other parts of the system behave differently if I change this? | Function Name | `-depth` |
| `what-if` | **Impact Simulation.** Computes the impact of hypothetical node removals (Severed Edges, Orphaned Nodes, etc.) for Strangler Fig planning. | Function Name/ID | `-target2 <Target 2>` |
| `hotspots` | **Risk Analysis.** Find functions with high complexity that change frequently. | (Ignored) | `-module <regex>` |
| `globals` | **State Analysis.** Find global variables used by a function. | Function Name | |
| `seams` | **Architecture.** Identify Pinch Points (chokepoints with high internal fan-in and high volatile fan-out). | (Ignored) | `-module <regex>` |
| `semantic-seams` | **Architecture.** Identify SRP violations and conceptual seams within a single file/class using vector embeddings. | (Ignored) | `-similarity <float>` |
| `locate-usage` | **Trace.** Find path/usage between two functions. | Function 1 | `-target2 <Function 2>` |
| `fetch-source` | **Read.** Fetch the source code of a function by ID/Name. | Function Name | |
| `explore-domain` | **Discovery.** Explore the domain model around a concept. | Concept/Entity Name | |
| `traverse` | **Raw Traversal.** Explore graph relationships directly. | Node ID / Name | `-edge-types`, `-direction`, `-depth` |
| `cypher` | **Advanced.** Run a raw, read-only Cypher query directly against the Neo4j database. | (Ignored) | `-cypher "<query>"` |
| `status` | **Verification.** Check the git commit hash stored in the graph. | (None) | |

### 4. Web Visualizer (HTTP Server)
The GraphDB skill includes a web-based visualizer for exploring the graph, viewing clusters, and performing interactive queries.

**Command:**
```bash
${graphdb_bin} serve -port 8080
```
*   *Options:*
    *   `-port`: The port to run the server on (default: `8080`).
    *   `-location`: GCP Location for semantic searches (default: `global`).
    *   `-model`: Embedding model to use for semantic searches (default: loaded from `.env`).

**Operational Note:**
When running the server in a remote environment (e.g., a Cloud VM), you may need to use SSH port forwarding to access the UI locally:
`ssh -L 8080:localhost:8080 your-vm-address`

## Operational Guidelines
*   **Output Parsing:** The tool returns JSON. Parse it and present a concise summary (bullet points, mermaid diagrams, or tables).
*   **Exact Names:** Structural queries (`neighbors`, `impact`, `coverage`, `what-if`) are **polymorphic**. You can provide the Node `ID` (e.g., `Function:Namespace.Class.Method:()`), the `fqn` (e.g., `Namespace.Class.Method`), or the simple `name` (e.g., `Method`).
    *   **Recommendation:** Use the `fqn` for the most robust results across overloads and distinct modules.
    *   **Test Analysis:** Always prefer `coverage` over `neighbors` when specifically looking for tests, as it leverages explicit links from `enrich-tests`.
    *   **Impact Analysis:** Use `impact` for general risk assessment and `what-if` for simulation-based planning (e.g., Strangler Fig pattern).
    *   **CRITICAL RULE:** If you only have a partial name or an ambiguous symbol from the user, **DO NOT use `grep` or text search** to find the fully qualified name. Instead, you MUST use `search-similar` (semantic search) first to locate the exact node `ID` or `fqn` before running structural queries.
*   **Context:** Always mention the source file and line number when discussing a function.
*   **Missing Data:** If a query returns empty, verify the spelling of the function/module name or try a semantic search.
