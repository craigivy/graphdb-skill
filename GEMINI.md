# GraphDB Skill Ecosystem

## 📖 Project Overview

This workspace hosts the **GraphDB Skill**, a powerful subsystem for the Gemini CLI designed to analyze, visualize, and assist in the modernization of large legacy codebases.

It employs a **Hybrid Architecture** that combines:
1.  **Code Property Graph (CPG):** A Neo4j database representing precise structural relationships (Calls, Inheritance, Variable Usage) extracted from source code (C++, C#, VB.NET, SQL, etc.).
2.  **Vector Embeddings:** Semantic understanding of code functions to identify implicit links and "conceptual" dependencies that static analysis misses. We standardize on **`gemini-embedding-001`** with **768 dimensions** for all embeddings to ensure compatibility across the ecosystem.
3.  **Repository Planning Graph (RPG):** An AI-generated "Intent Layer" that hierarchically groups low-level code implementations into higher-level business domains and semantic features. It acts as a bridge between the structural code graph and the original developer/business intent.

## 📂 Repository Structure

*   **`.gemini/skills/graphdb/`**: The core skill. Contains logic for parsing code (Tree-sitter), building the graph, and querying it.
*   **`plans/`**: Strategic documentation and architectural plans.

## 🚀 Getting Started

### Prerequisites

*   **Node.js**: v20+
*   **Neo4j Community Edition**: v5.x (Local) with Vector Index support (v5.11+).
*   **Google Cloud Project**: For Vertex AI embeddings (required for Vector Search).

### Configuration (`.env`)
Ensure a `.env` file exists in the project root, refer to @README.md for specifics.

## Building the GraphDB CLI

The core skill relies on a compiled Go binary. Because the CLI is used across different environments (Linux developers, Windows users), we compile binaries for both OS platforms simultaneously.

Because of our CGO dependency (`tree-sitter`), cross-compiling for Windows from a Linux environment requires **Zig** as the C cross-compiler.

**1. Install Zig Locally (One-time Setup)**
If you haven't already, download and extract Zig to the local tools directory:
```bash
mkdir -p .gemini/tools && cd .gemini/tools
curl -L -O https://ziglang.org/download/0.13.0/zig-linux-x86_64-0.13.0.tar.xz
tar -xf zig-linux-x86_64-0.13.0.tar.xz
cd ../..
```

**2. Run the Full Build -- **CRITICAL** 
**NEVER BUILD DIRECTLY with go**, *ALWAYS* use the Makefile from the project root to build both Linux and Windows binaries. The Makefile will automatically handle the local Zig path, complex `CGO_ENABLED`, and target flags for the Windows build.

```bash
make build-all
```

**Output:**
The binaries will be automatically placed in the required skill folder:
*   Linux: `.gemini/skills/graphdb/scripts/graphdb`
*   Windows: `.gemini/skills/graphdb/scripts/graphdb-win.exe`

## Testing

**CRITICAL:**
Always run the project tests using the Makefile target rather than `go test` directly.

```bash
make test
```

