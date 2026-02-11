package sign

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/keybase/go-crypto/openpgp"
	"github.com/keybase/go-crypto/openpgp/armor"
	"github.com/keybase/go-crypto/openpgp/packet"
)

// PublicKeyFilename is the conventional name for the public key file in a release (e.g. for GitHub).
const PublicKeyFilename = "release-key.asc"

// LoadSigningEntity reads an armored private key from keyFilePath, optionally selects by keyID (hex string),
// and decrypts the private key with passphrase. Returns the entity ready for signing.
func LoadSigningEntity(keyFilePath, keyID string, passphrase []byte) (*openpgp.Entity, error) {
	f, err := os.Open(keyFilePath)
	if err != nil {
		return nil, fmt.Errorf("open signing key file: %w", err)
	}
	defer f.Close()

	el, err := openpgp.ReadArmoredKeyRing(f)
	if err != nil {
		return nil, fmt.Errorf("read armored key ring: %w", err)
	}

	var entity *openpgp.Entity
	if keyID != "" {
		id, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(keyID), "0x"), 16, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid signing key ID %q: %w", keyID, err)
		}
		for _, e := range el {
			if e.PrimaryKey != nil && e.PrimaryKey.KeyId == id && e.PrivateKey != nil {
				entity = e
				break
			}
		}
		if entity == nil {
			return nil, fmt.Errorf("no private key found with ID %s in key file", keyID)
		}
	} else {
		for _, e := range el {
			if e.PrivateKey != nil {
				entity = e
				break
			}
		}
		if entity == nil {
			return nil, fmt.Errorf("no private key found in key file %s", keyFilePath)
		}
	}

	// Decrypt private key if encrypted
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Decrypt(passphrase); err != nil {
			return nil, fmt.Errorf("decrypt private key: %w", err)
		}
	}
	for i := range entity.Subkeys {
		if entity.Subkeys[i].PrivateKey != nil && entity.Subkeys[i].PrivateKey.Encrypted {
			if err := entity.Subkeys[i].PrivateKey.Decrypt(passphrase); err != nil {
				return nil, fmt.Errorf("decrypt subkey: %w", err)
			}
		}
	}

	return entity, nil
}

// ExportPublicKey writes the entity's public key in armored form to outPath (e.g. versionDir/release-key.asc).
// Use this so release consumers can verify signatures without the private key.
func ExportPublicKey(entity *openpgp.Entity, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create public key file: %w", err)
	}
	defer f.Close()
	w, err := armor.Encode(f, openpgp.PublicKeyType, nil)
	if err != nil {
		return fmt.Errorf("armor encode: %w", err)
	}
	if err := entity.Serialize(w); err != nil {
		w.Close()
		return fmt.Errorf("serialize public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close armor writer: %w", err)
	}
	return nil
}

// SignFile creates a detached signature of artifactPath and writes it to sigPath.
// If armored is true, the signature is ASCII-armored (.asc); otherwise binary (.sig).
func SignFile(entity *openpgp.Entity, artifactPath, sigPath string, armored bool) error {
	art, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer art.Close()

	sig, err := os.Create(sigPath)
	if err != nil {
		return fmt.Errorf("create signature file: %w", err)
	}
	defer sig.Close()

	config := &packet.Config{}
	if armored {
		err = openpgp.ArmoredDetachSign(sig, entity, art, config)
	} else {
		err = openpgp.DetachSign(sig, entity, art, config)
	}
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	return nil
}

// VerifySignature checks that sigPath is a valid detached signature for artifactPath
// using the signer's public key from entity. armored is true if the signature is ASCII-armored.
func VerifySignature(entity *openpgp.Entity, artifactPath, sigPath string, armored bool) error {
	art, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer art.Close()

	sig, err := os.Open(sigPath)
	if err != nil {
		return fmt.Errorf("open signature file: %w", err)
	}
	defer sig.Close()

	keyring := openpgp.EntityList{entity}
	var signer *openpgp.Entity
	if armored {
		signer, err = openpgp.CheckArmoredDetachedSignature(keyring, art, sig)
	} else {
		signer, err = openpgp.CheckDetachedSignature(keyring, art, sig)
	}
	if err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	if signer == nil {
		return fmt.Errorf("verify signature: signer not found")
	}
	return nil
}

// SignAndVerify signs the artifact at artifactPath, writes the signature to sigPath,
// then verifies the signature. sigExt should be ".sig" or ".asc".
func SignAndVerify(entity *openpgp.Entity, artifactPath, sigPath, sigExt string) error {
	armored := strings.HasSuffix(strings.ToLower(sigExt), ".asc")
	if err := SignFile(entity, artifactPath, sigPath, armored); err != nil {
		return err
	}
	if err := VerifySignature(entity, artifactPath, sigPath, armored); err != nil {
		_ = os.Remove(sigPath)
		return err
	}
	return nil
}

// SignaturePath returns the path for the signature file (artifactPath + ext).
func SignaturePath(artifactPath, ext string) string {
	return artifactPath + ext
}

// SignArtifacts signs each artifact in versionDir and verifies each signature.
// artifactNames are basenames (e.g. "myapp", "myapp.zst"). sigExt is ".sig" or ".asc".
func SignArtifacts(entity *openpgp.Entity, versionDir string, artifactNames []string, sigExt string) error {
	for _, name := range artifactNames {
		artifactPath := filepath.Join(versionDir, name)
		sigPath := SignaturePath(artifactPath, sigExt)
		if err := SignAndVerify(entity, artifactPath, sigPath, sigExt); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
