package store

import "fmt"

// IdentityKind is the class of caller a write is attributed to. It is
// observed, never declared: the future socket layer builds it from
// SO_PEERCRED plus /proc ancestry (AGENT-CONTRACT.md §Observed identity),
// not from anything a caller sends.
type IdentityKind int

const (
	// IdentityHuman is the panel/CLI path — the only route to human-declared.
	IdentityHuman IdentityKind = iota
	// IdentityAgent is an agent process talking over the socket.
	IdentityAgent
	// IdentityInference is the daemon's own inference pass.
	IdentityInference
)

// Identity is the caller a write is attributed to. The store derives
// records.tier from Kind; there is no way to pass a tier directly.
type Identity struct {
	Kind IdentityKind
	// Actor labels the caller for columns that record who (not what tier),
	// such as edges.declared_by and records.promoter: a session id,
	// "human", or "daemon".
	Actor string
}

// tier maps an Identity's Kind to the records.tier value it is entitled to.
// This is the ONLY place a tier is derived; InsertRecord has no tier
// parameter and calls only this.
func (k IdentityKind) tier() (Tier, error) {
	switch k {
	case IdentityHuman:
		return TierHumanDeclared, nil
	case IdentityAgent:
		return TierAgentDeclared, nil
	case IdentityInference:
		return TierInferred, nil
	default:
		return "", fmt.Errorf("store: unknown identity kind %d", k)
	}
}
