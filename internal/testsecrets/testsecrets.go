// Package testsecrets holds obviously fake credentials, one per family the
// redactor must catch, shared by the redaction tests of every write path.
// Nothing here is a real credential; values are assembled at init so a
// secret scanner reading the source sees no literal key shape.
package testsecrets

// Fake is one fake credential family. Text is what a user would type or a
// tool would print; Secret is the substring that must never be stored; Keep
// (if set) is a substring that must survive redaction (an env var name).
type Fake struct {
	Family string
	Text   string
	Secret string
	Keep   string
}

func fake(family, secret string) Fake {
	return Fake{Family: family, Text: "key " + secret + " end", Secret: secret}
}

func env(family, name, value string) Fake {
	return Fake{Family: family, Text: name + "=" + value + " tail", Secret: value, Keep: name + "="}
}

// All returns the table, one entry per family.
func All() []Fake {
	const filler = "FAKEFAKEFAKEFAKE0123456789"
	return []Fake{
		fake("anthropic", "sk-"+"ant-api03-"+filler+"_-abc"),
		fake("openai-proj", "sk-"+"proj-"+filler+"_-abc"),
		fake("openai-svcacct", "sk-"+"svcacct-"+filler+"_-abc"),
		fake("github-pat-classic", "gh"+"p_"+filler),
		fake("github-pat-fine", "github"+"_pat_"+filler+"_abc"),
		fake("slack", "xo"+"xb-0000000000-"+filler),
		fake("jwt", "eyJ"+"hbGciOiJub25lIn0.eyJ"+"zdWIiOiJmYWtlIn0.ZmFrZXNpZ25hdHVyZQ"),
		env("env-anthropic", "ANTHROPIC_API_KEY", "fakevalue"+filler),
		env("env-gh-token", "GH_TOKEN", "fakevalue"+filler),
	}
}
