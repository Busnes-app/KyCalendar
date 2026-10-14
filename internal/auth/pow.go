package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// PoWChallenge represents a client-side SHA256 puzzle matching the KySecurity standard.
type PoWChallenge struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
	MaxNumber int    `json:"maxnumber"`
	ExpiresAt int64  `json:"expires_at"`
	Signature string `json:"signature"`
}

// PoWSolution represents the client's submitted answer.
type PoWSolution struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
	Number    int    `json:"number"`
	MaxNumber int    `json:"maxnumber"`
	ExpiresAt int64  `json:"expires_at"`
	Signature string `json:"signature"`
}

// powKey separates the PoW signing key from the session secret it is derived from.
func powKey(secret string) string {
	return string(crypto.DeriveKey([]byte(secret), "kycalendar:pow"))
}

// powSpentCap bounds PoWSpent. Entries live at most five minutes and each costs a login past
// the per-IP limiter, so only a flood from many addresses reaches it.
const powSpentCap = 100000

// PoWSpent records verified solutions until their challenge expires, so each solution admits
// one request. The zero value is ready to use.
type PoWSpent struct {
	mu    sync.Mutex
	salts map[string]int64 // salt -> challenge ExpiresAt
}

// spend reports whether salt was unspent and records it. When full it drops expired entries,
// then live ones at random. Refusing instead would let a flood fail every login; eviction only
// lets one already-solved puzzle be presented again, still behind every login limiter.
func (p *PoWSpent) spend(salt string, expiresAt, now int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.salts == nil {
		p.salts = map[string]int64{}
	}
	if _, used := p.salts[salt]; used {
		return false
	}
	if len(p.salts) >= powSpentCap {
		for s, exp := range p.salts {
			if exp < now {
				delete(p.salts, s)
			}
		}
		for s := range p.salts {
			if len(p.salts) < powSpentCap {
				break
			}
			delete(p.salts, s)
		}
	}
	p.salts[salt] = expiresAt
	return true
}

// GeneratePoWChallenge creates a puzzle of given difficulty (e.g. 5000 to 50000 max iterations).
func GeneratePoWChallenge(difficulty int, signingSecret string) (*PoWChallenge, error) {
	if difficulty <= 0 {
		difficulty = 20000
	}

	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return nil, err
	}
	salt := hex.EncodeToString(saltBytes)

	// Pick a random winning number in range [1, difficulty]
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(difficulty)))
	if err != nil {
		return nil, err
	}
	targetNumber := int(nBig.Int64()) + 1

	targetStr := fmt.Sprintf("%s%d", salt, targetNumber)
	h := sha256.Sum256([]byte(targetStr))
	challenge := hex.EncodeToString(h[:])

	challengeExpires := time.Now().Add(5 * time.Minute).Unix()
	signaturePayload := fmt.Sprintf("%s:%s:%d:%d", salt, challenge, difficulty, challengeExpires)
	return &PoWChallenge{
		Algorithm: "SHA-256",
		Salt:      salt,
		Challenge: challenge,
		MaxNumber: difficulty,
		ExpiresAt: challengeExpires,
		Signature: crypto.ComputeHMACSHA256([]byte(signaturePayload), powKey(signingSecret)),
	}, nil
}

// VerifyPoWSolution decodes and validates the client's base64-encoded JSON solution and spends
// it in spent, so presenting it again fails.
func VerifyPoWSolution(solutionBase64, signingSecret string, spent *PoWSpent) bool {
	data, err := base64.StdEncoding.DecodeString(solutionBase64)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(solutionBase64)
		if err != nil {
			return false
		}
	}

	var sol PoWSolution
	if err := json.Unmarshal(data, &sol); err != nil {
		return false
	}

	now := time.Now().Unix()
	if sol.Algorithm != "SHA-256" || sol.Salt == "" || sol.Challenge == "" || sol.Signature == "" ||
		sol.Number < 1 || sol.MaxNumber < 1 || sol.Number > sol.MaxNumber || sol.ExpiresAt < now {
		return false
	}
	signaturePayload := sol.Salt + ":" + sol.Challenge + ":" + strconv.Itoa(sol.MaxNumber) + ":" + strconv.FormatInt(sol.ExpiresAt, 10)
	if !crypto.VerifyHMACSHA256([]byte(signaturePayload), powKey(signingSecret), sol.Signature) {
		return false
	}

	targetStr := fmt.Sprintf("%s%d", sol.Salt, sol.Number)
	h := sha256.Sum256([]byte(targetStr))
	actualChallenge := hex.EncodeToString(h[:])

	return actualChallenge == sol.Challenge && spent.spend(sol.Salt, sol.ExpiresAt, now)
}
