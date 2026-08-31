<!-- BEGIN ai-protocol -->
# Agent Instructions

This repository's operating protocol lives in `.session.md`.

Before doing substantive work, read `.session.md` in full and follow it. It
covers startup context loading, session setup, session lifecycle, skill loading,
Worktrunk branching, session journaling, file schemas, architecture, and process
expectations.

If `.session.md` is missing, stop and tell the user the session protocol is not
installed correctly.
<!-- END ai-protocol -->

# Go Best Practices

Rules are grouped by category and numbered for reference. Cite them by ID in
reviews and feedback: "this violates A1", "apply D4 here".

## A — Architecture

- **A1**: Follow hexagonal architecture strictly. Business logic must run and
  test without side effects. Put all I/O behind ports; adapters implement them.
- **A2**: Build each adapter for one purpose. Do not write broad adapters that
  wrap an entire service surface.
- **A3**: A package does one obvious thing and keeps a strong contract. Export
  a thin set of types and functions; keep the core logic unexported.
- **A4**: Package names are short, pronounceable, and obvious. Do not join
  several words into one long name. Duplicate names across the tree are fine
  (two packages can each have a `mocks/` subpackage); disambiguate with import
  aliases at the call site.

## T — Testing

- **T1**: Test at three layers: unit tests for functions, integration tests
  with mock adapters, and end-to-end tests against live services (for example,
  testcontainers).
- **T2**: Never write a mock by hand. Generate all mocks with `mockery`.
- **T3**: Generate mocks for every adapter by default. Mocks live in a `mocks/`
  subpackage under the adapter's primary package.

## R — Readability

- **R1**: Code must be easy to follow. This cuts both ways: no compressed
  one-liners that need decoding, and no layered abstractions that hide simple
  logic.
- **R2**: Hard cap of 1,000 lines per Go source file. Before reaching it, split
  into two well-named files or extract a new package.
- **R3**: Never reimplement a standard library function. Check what the
  project's Go version ships before assuming a function does not exist.

## P — Performance

- **P1**: When touching or creating code, decide whether it sits on a critical
  path. If it does, make extra passes until it meets a high bar for efficiency.
- **P2**: Do not abuse the heap. Watch what gets copied. Stream large data
  (`io.Reader`/`io.Writer` piping) instead of buffering it into memory.

## D — Documentation

- **D1**: Every function, type, and struct field gets a Godoc comment, exported
  or not. This feeds IDE intellisense and helps developers move through the
  code.
- **D2**: Functions that form a public API boundary get extra effort: thorough
  Godoc plus examples where they help.
- **D3**: Use inline comments sparingly. Reserve them for genuinely complex
  code or code that would otherwise mislead, such as a non-obvious workaround.
- **D4**: Every package has a `doc.go` with a package-level Godoc comment. No
  exceptions.
- **D5**: All user-facing documentation lives in `docs/`, follows the
  [Diátaxis](https://diataxis.fr/) conventions (tutorials, how-to guides,
  reference, explanation), and is written in the Plain Language style.
- **D6**: When a change affects behavior a user can see and the repository has
  user documentation, update the docs in the same PR as the change.

## I — Types and Interfaces

- **I1**: Create types for domain terms instead of passing plain types around.
  Prefer `type ProjectName string` over accepting `projectName string` in every
  function.
- **I2**: Accept interfaces, return concrete types. Deviate only when following
  the idiom makes the API clearly worse to use.

## E — Errors and Reliability

- **E1**: Define sentinel errors when callers need to inspect why something
  failed. Keep them high-level: a sentinel wraps the plain errors bubbling up
  from below. Do not scatter sentinels everywhere.
- **E2**: Check pointers and interfaces for nil before use. If a value was
  already checked upstream, do not check it again.
- **E3**: Do not give up and return an error when a retry is the acceptable
  behavior. Failing on the first transient error is the easy way out.

## L — Dependencies

- **L1**: Prefer mature, well-tested libraries. Never import an obviously
  abandoned one. Weigh size against need: writing a small piece yourself beats
  importing 5% of a very large library.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **release** (5429 symbols, 21628 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/release/context` | Codebase overview, check index freshness |
| `gitnexus://repo/release/clusters` | All functional areas |
| `gitnexus://repo/release/processes` | All execution flows |
| `gitnexus://repo/release/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
