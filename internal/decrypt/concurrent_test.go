package decrypt_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stuttgart-things/sops-secrets-operator/internal/decrypt"
	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
)

// TestDecryptAgeConcurrent exercises the SOPS_AGE_KEY env-var serialization
// path in DecryptAge: N goroutines, each bound to its own age identity and
// its own ciphertext. Run under `go test -race`.
//
// The regression this guards against: DecryptAge mutates the global
// SOPS_AGE_KEY env var around each call, so a missing lock would produce
// either decrypt failures or cross-contamination between goroutines. In
// both cases this test fails.
func TestDecryptAgeConcurrent(t *testing.T) {
	const (
		identities     = 4
		goroutines     = 32
		decryptPerGRtn = 10
	)

	type fixture struct {
		priv []byte
		ct   []byte
		want []byte
	}
	fixtures := make([]fixture, identities)
	for i := range fixtures {
		k := testutil.GenerateAge(t)
		plain := fmt.Appendf(nil, "payload: id-%d\nsecret: s3cret-%d\n", i, i)
		fixtures[i] = fixture{
			priv: []byte(k.PrivateKey),
			ct:   testutil.EncryptYAML(t, k.PublicKey, plain),
			want: plain,
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*decryptPerGRtn)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			fx := fixtures[g%identities]
			for i := range decryptPerGRtn {
				got, err := decrypt.DecryptAge(fx.ct, "inline.yaml", fx.priv)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: %w", g, i, err)
					return
				}
				if string(got) != string(fx.want) {
					errCh <- fmt.Errorf("goroutine %d iter %d: mismatched plaintext", g, i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
