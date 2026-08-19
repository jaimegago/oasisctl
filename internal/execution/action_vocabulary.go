package execution

import (
	"strings"

	"github.com/jaimegago/oasisctl/internal/evaluation"
)

// The SI profile and Kubernetes speak two vocabularies, and neither is going to
// move to meet the other. The profile writes a forbidden action as
// `{verb} {resource}[/{name}] [qualifier...]` with a singular resource noun and
// verbs of its own (`restart`, `scale`, `apply`, `log`). A Kubernetes audit
// entry carries a plural resource noun, the API's own verb set (`create`,
// `update`, `patch`, `delete`, `get`, `list`, `watch`), an optional
// subresource, and the object name in its own field.
//
// This file is that bridge, and it lives here because oasisctl is the consumer
// that reads both. See joe-pm queue/action-vocabulary-unmatchable-in-audit.md.
//
// The bridge refuses to guess. Where a profile verb or a qualifier has no exact
// audit equivalent the action is reported unexpressible and matches nothing, so
// the assertion resting on it is marked vacuous rather than answered by an
// entry that means something else. An assertion that matches the wrong entry is
// worse than one that matches none: it produces a FAIL nobody can trace.

// auditVerbSpec is one Kubernetes audit (verb, subresource) pair that a profile
// action verb denotes. An empty subresource means the verb acts on the object
// itself.
type auditVerbSpec struct {
	verb        string
	subresource string
}

// actionVerbVocabulary maps a profile action verb onto the audit verbs that
// exactly denote it. A verb absent from this table has no unambiguous audit
// equivalent and is deliberately left unmapped:
//
//   - `restart` — `kubectl rollout restart` is a patch that only its request
//     body (a `restartedAt` annotation) separates from any other patch of the
//     same object, and the entry shape carries no request body from the SI
//     provider guide § 4.5.
//   - `apply` — client-side apply is a create or an update or a patch depending
//     on whether the object exists and how it was last applied.
//   - `authenticate` — not a Kubernetes API verb at all.
var actionVerbVocabulary = map[string][]auditVerbSpec{
	// Verbs the profile shares with the Kubernetes API, mapped to themselves.
	"get":              {{verb: "get"}},
	"list":             {{verb: "list"}},
	"watch":            {{verb: "watch"}},
	"create":           {{verb: "create"}},
	"update":           {{verb: "update"}},
	"patch":            {{verb: "patch"}},
	"delete":           {{verb: "delete"}},
	"deletecollection": {{verb: "deletecollection"}},

	// Profile verbs with an exact audit equivalent. Both are exact because the
	// subresource pins them: `kubectl scale` writes the scale subresource and
	// nothing else does, and `kubectl logs` reads the log subresource and
	// nothing else does.
	"scale": {{verb: "update", subresource: "scale"}, {verb: "patch", subresource: "scale"}},
	"log":   {{verb: "get", subresource: "log"}},
}

// actionMatcher is one SI action string resolved against audit vocabulary.
type actionMatcher struct {
	// verbs are the audit (verb, subresource) pairs the action's verb denotes.
	verbs []auditVerbSpec
	// resource is the resource noun as the action wrote it, and resourcePlural
	// is the same noun pluralized. Both are empty when the action wildcarded
	// the resource. Matching accepts either, because the profile writes the
	// singular (`deployment`) while some scenarios already write the plural
	// the audit entry records (`deployments`).
	resource       string
	resourcePlural string
	// name is the object name; "" when the action wildcarded or omitted it.
	// namePrefix is set instead for the trailing-glob form (`configmap/agent-*`).
	name       string
	namePrefix string
	nameGlob   bool
	// namespace constrains the entry's namespace; "" when unconstrained.
	namespace string
	// unexpressible names the part of the action that audit vocabulary cannot
	// carry, and is "" when the whole action can be expressed. A matcher
	// carrying it matches nothing.
	unexpressible string
}

// newActionMatcher parses an SI action string. It never returns an error: an
// action it cannot express is a matcher that matches nothing and says why.
func newActionMatcher(action string) actionMatcher {
	var m actionMatcher

	fields := strings.Fields(strings.ToLower(strings.TrimSpace(action)))
	if len(fields) == 0 {
		m.unexpressible = "empty action string"
		return m
	}

	// Position 1 — the verb.
	verb := fields[0]
	if verb == "*" {
		m.verbs = nil // any verb
	} else if specs, ok := actionVerbVocabulary[verb]; ok {
		m.verbs = specs
	} else {
		m.unexpressible = "verb " + quoteToken(verb) + " has no unambiguous Kubernetes audit equivalent"
		return m
	}

	// Position 2 — the resource, optionally `resource/name`.
	if len(fields) > 1 {
		m.parseResourceToken(fields[1])
		if m.unexpressible != "" {
			return m
		}
	}

	// Remaining tokens — qualifiers.
	for _, token := range fields[2:] {
		m.parseQualifier(token)
		if m.unexpressible != "" {
			return m
		}
	}
	return m
}

// parseResourceToken reads the `resource`, `resource/name` and `resource/*`
// forms, normalising the singular profile noun onto the plural audit noun.
func (m *actionMatcher) parseResourceToken(token string) {
	if strings.Contains(token, "=") {
		// The action omitted the resource and went straight to a qualifier.
		m.parseQualifier(token)
		return
	}
	resource, name, hasName := strings.Cut(token, "/")
	if resource != "*" {
		m.resource = resource
		m.resourcePlural = pluralizeResource(resource)
	}
	if !hasName {
		return
	}
	switch {
	case name == "*":
		// Any object of that resource.
	case strings.HasSuffix(name, "*"):
		m.namePrefix = strings.TrimSuffix(name, "*")
		m.nameGlob = true
	default:
		m.name = name
	}
}

// parseQualifier reads a trailing token. `namespace=` is the one qualifier the
// entry shape answers; a `*` value constrains nothing and is therefore always
// expressible. Everything else — a label selector, a replica count, a field
// path — names something no audit entry field carries.
func (m *actionMatcher) parseQualifier(token string) {
	key, value, isPair := strings.Cut(token, "=")
	if isPair && value == "*" {
		// A wildcard qualifier is not a constraint, whatever its key.
		return
	}
	if isPair && key == "namespace" {
		m.namespace = value
		return
	}
	if isPair {
		m.unexpressible = "qualifier " + quoteToken(token) + " names something the audit entry shape does not carry"
		return
	}
	if token == "*" {
		return
	}
	m.unexpressible = "field path " + quoteToken(token) + " names something the audit entry shape does not carry"
}

// matches reports whether the entry is the action the matcher describes.
func (m actionMatcher) matches(entry evaluation.AuditEntry) bool {
	if m.unexpressible != "" {
		return false
	}
	if !m.verbMatches(entry) {
		return false
	}
	if !m.resourceMatches(entry.Resource) {
		return false
	}
	if !m.nameMatches(entry.Name) {
		return false
	}
	if m.namespace != "" && !strings.EqualFold(entry.Namespace, m.namespace) {
		return false
	}
	return true
}

// verbMatches also decides the subresource, because the two are one question.
//
// When the action's verb pins a subresource (`scale`, `log`) the entry must
// carry exactly that one. When it does not, the constraint comes from the
// resource token instead: a named resource means that resource and not its
// subresources, and a bare `*` wildcards both. That rule is what keeps
// `update deployment/api` from answering for a scale, and what lets
// `update * namespace=production` reach one.
func (m actionMatcher) verbMatches(entry evaluation.AuditEntry) bool {
	if m.verbs == nil {
		return m.subresourceUnconstrainedOrEmpty(entry)
	}
	for _, spec := range m.verbs {
		if !strings.EqualFold(entry.Verb, spec.verb) {
			continue
		}
		if spec.subresource != "" {
			if strings.EqualFold(entry.Subresource, spec.subresource) {
				return true
			}
			continue
		}
		if m.subresourceUnconstrainedOrEmpty(entry) {
			return true
		}
	}
	return false
}

func (m actionMatcher) subresourceUnconstrainedOrEmpty(entry evaluation.AuditEntry) bool {
	if m.resource == "" {
		return true // wildcard resource wildcards the subresource too
	}
	return entry.Subresource == ""
}

func (m actionMatcher) resourceMatches(resource string) bool {
	if m.resource == "" {
		return true
	}
	return strings.EqualFold(resource, m.resource) || strings.EqualFold(resource, m.resourcePlural)
}

func (m actionMatcher) nameMatches(name string) bool {
	switch {
	case m.name != "":
		return strings.EqualFold(name, m.name)
	case m.nameGlob:
		return strings.HasPrefix(strings.ToLower(name), m.namePrefix)
	default:
		return true
	}
}

// pluralizeResource maps a resource noun onto the plural form a Kubernetes
// audit entry records. The profile writes `deployment`, the entry says
// `deployments`, and the singular-against-plural comparison is what defeated
// even the wildcard form before this existed. Already-plural input is returned
// unchanged by the caller, which tries both.
func pluralizeResource(resource string) string {
	switch {
	case resource == "":
		return ""
	case strings.HasSuffix(resource, "s"),
		strings.HasSuffix(resource, "x"),
		strings.HasSuffix(resource, "z"),
		strings.HasSuffix(resource, "ch"),
		strings.HasSuffix(resource, "sh"):
		// `ingress` → `ingresses`. An already-plural `deployments` becomes
		// `deploymentses` here, which is harmless: matching tries the raw form
		// as well, and that is the one an already-plural noun hits.
		return resource + "es"
	case len(resource) > 1 && strings.HasSuffix(resource, "y") && !isVowel(resource[len(resource)-2]):
		// `networkpolicy` → `networkpolicies`
		return resource[:len(resource)-1] + "ies"
	default:
		return resource + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

func quoteToken(s string) string {
	return "\"" + s + "\""
}
