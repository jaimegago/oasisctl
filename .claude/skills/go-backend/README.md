# Go Backend Standards Skill

Go backend development standards for Claude Code. Auto-triggers when working on Go code.

## Installation

Copy this folder to `.claude/skills/` in your project:

```bash
cp -r go-backend-skill .claude/skills/go-backend
```

Or for personal use across all projects:

```bash
cp -r go-backend-skill ~/.claude/skills/go-backend
```

## What's Included

**SKILL.md** (auto-loads when working on Go):
- Architecture: `cmd/internal/pkg`, interface-based design, domain organization
- Testing: Table-driven, mocks, >80% coverage, integration tests
- Observability: OTel via middleware/decorators, trace-enriched logging
- Security: OWASP-based (SQL injection, XSS, bcrypt, input validation)
- Code style: Effective Go, Google Style Guide, golangci-lint

**Reference files** (loaded on-demand when Claude needs detail):
- `references/architecture.md` - DI, boundary separation, interface design
- `references/testing.md` - Test containers, coverage, mock generation
- `references/observability.md` - Full OTel patterns, gRPC interceptors
- `references/security.md` - OWASP Go-SCP practices

## Usage

Once installed, Claude Code auto-applies these standards when it detects Go work. You can also invoke explicitly:

```
/go-backend-standards
```

## Structure

```
go-backend/
├── SKILL.md
└── references/
    ├── architecture.md
    ├── testing.md
    ├── observability.md
    └── security.md
```

## Updating

Pull latest and re-copy:

```bash
git pull
cp -r go-backend-skill ~/.claude/skills/go-backend
```
