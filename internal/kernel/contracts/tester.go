package contracts

import "context"

// Tester is a provider that can check its settings without changing
// anything people care about: a deploy target signing in and finding where
// it deploys to, an ACME account reaching its CA. Test says what it found,
// or fails with a Problem saying what is wrong. A type whose providers are
// Testers says so with providers.Type.Testable.
type Tester interface {
	Test(ctx context.Context) (Message, error)
}
