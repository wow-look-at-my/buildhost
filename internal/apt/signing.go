package apt

import (
	"bytes"
	"crypto"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

type Signer struct {
	mu     sync.RWMutex
	entity *openpgp.Entity
}

func NewSigner(dataDir string) *Signer {
	s := &Signer{}
	keyPath := filepath.Join(dataDir, "apt-signing.key")
	if err := s.loadOrGenerate(keyPath); err != nil {
		slog.Error("apt signing key setup failed", "err", err)
	}
	return s
}

func (s *Signer) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entity != nil
}

func (s *Signer) PublicKeyArmored() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entity == nil {
		return nil, fmt.Errorf("no signing key available")
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := s.entity.Serialize(w); err != nil {
		w.Close()
		return nil, err
	}
	w.Close()
	return buf.Bytes(), nil
}

func (s *Signer) ClearSign(data []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entity == nil {
		return nil, fmt.Errorf("no signing key available")
	}
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, s.entity.PrivateKey, &packet.Config{
		DefaultHash: crypto.SHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("clearsign encode: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, fmt.Errorf("clearsign write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("clearsign close: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *Signer) DetachedSign(data []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entity == nil {
		return nil, fmt.Errorf("no signing key available")
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, s.entity, bytes.NewReader(data), &packet.Config{
		DefaultHash: crypto.SHA256,
	}); err != nil {
		return nil, fmt.Errorf("detach sign: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *Signer) loadOrGenerate(keyPath string) error {
	if data, err := os.ReadFile(keyPath); err == nil {
		return s.loadKey(data)
	}
	return s.generateAndSave(keyPath)
}

func (s *Signer) loadKey(data []byte) error {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("read key: %w", err)
	}
	if len(entities) == 0 {
		return fmt.Errorf("no key found in keyring")
	}
	s.mu.Lock()
	s.entity = entities[0]
	s.mu.Unlock()
	return nil
}

func (s *Signer) generateAndSave(keyPath string) error {
	entity, err := openpgp.NewEntity("Buildhost", "APT Release signing", "apt@buildhost.local", &packet.Config{
		RSABits:     4096,
		DefaultHash: crypto.SHA256,
	})
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}

	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return err
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		w.Close()
		return fmt.Errorf("serialize key: %w", err)
	}
	w.Close()

	if err := os.WriteFile(keyPath, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	s.mu.Lock()
	s.entity = entity
	s.mu.Unlock()
	slog.Info("generated APT signing key", "path", keyPath)
	return nil
}

func (s *Signer) Fingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entity == nil {
		return ""
	}
	return fmt.Sprintf("%X", s.entity.PrimaryKey.Fingerprint)
}

