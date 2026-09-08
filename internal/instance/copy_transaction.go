package instance

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/modelrouter"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	copyTransactionFile = ".copy-transaction.json"
	copyPreparing       = "preparing"
	copyCommitting      = "committing"
	copyApplied         = "applied"
)

var copyStagePattern = regexp.MustCompile(`^\.copy-stage-[a-f0-9]{16}$`)
var credentialStageSuffixPattern = regexp.MustCompile(`^[0-9]+$`)

var copyConfigFiles = []string{".env", "model_router.json", "model_router_secrets.json", "dialogue.yml"}
var writeCopyFile = instanceconfig.WriteAtomicDurable

type copyTransaction struct {
	Version     int                               `json:"version"`
	Phase       string                            `json:"phase"`
	Stage       string                            `json:"stage"`
	Credentials []modelrouter.CredentialPromotion `json:"credentials,omitempty"`
}

func newCopyTransaction(id string, copied, removed []modelrouter.CredentialChange) (copyTransaction, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return copyTransaction{}, err
	}
	token := hex.EncodeToString(b)
	return copyTransaction{
		Version: 1,
		Phase:   copyPreparing,
		Stage:   ".copy-stage-" + token,
		Credentials: modelrouter.PlanCredentialPromotions(
			"guaitech.frostagent/transaction/"+id+"/"+token,
			copied,
			removed,
		),
	}, nil
}

func transactionPath(dir string) string { return filepath.Join(dir, copyTransactionFile) }

func writeCopyTransaction(dir string, transaction copyTransaction) (bool, error) {
	data, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return false, err
	}
	return writeCopyFile(transactionPath(dir), data, 0600)
}

func readCopyTransaction(dir, id string) (*copyTransaction, error) {
	data, err := os.ReadFile(transactionPath(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var transaction copyTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return nil, fmt.Errorf("解析快速配置事务失败: %w", err)
	}
	if err := validateCopyTransaction(id, transaction); err != nil {
		return nil, err
	}
	return &transaction, nil
}

func validateCopyTransaction(id string, transaction copyTransaction) error {
	if transaction.Version != 1 || !copyStagePattern.MatchString(transaction.Stage) || filepath.Base(transaction.Stage) != transaction.Stage {
		return fmt.Errorf("无效的快速配置事务")
	}
	if transaction.Phase != copyPreparing && transaction.Phase != copyCommitting && transaction.Phase != copyApplied {
		return fmt.Errorf("无效的快速配置事务阶段")
	}
	token := strings.TrimPrefix(transaction.Stage, ".copy-stage-")
	stagePrefix := "guaitech.frostagent/transaction/" + id + "/" + token + "/"
	seenTargets := make(map[string]bool, len(transaction.Credentials))
	for _, action := range transaction.Credentials {
		if !strings.HasPrefix(action.Target, "guaitech.frostagent/endpoint/") || strings.ContainsAny(action.Target, "\x00\r\n") {
			return fmt.Errorf("无效的快速配置凭据目标")
		}
		if seenTargets[action.Target] {
			return fmt.Errorf("快速配置凭据目标重复")
		}
		seenTargets[action.Target] = true
		if action.Delete {
			if action.StagedTarget != "" {
				return fmt.Errorf("删除凭据不能包含暂存目标")
			}
		} else {
			suffix := strings.TrimPrefix(action.StagedTarget, stagePrefix)
			if !strings.HasPrefix(action.StagedTarget, stagePrefix) || !credentialStageSuffixPattern.MatchString(suffix) {
				return fmt.Errorf("无效的快速配置暂存凭据")
			}
		}
	}
	return nil
}

func removeCopyStage(dir string, transaction copyTransaction) error {
	stageDir := filepath.Join(dir, transaction.Stage)
	if err := safeTree(stageDir); err != nil {
		return err
	}
	return os.RemoveAll(stageDir)
}

func abortCopyTransaction(dir string, transaction copyTransaction) error {
	err := modelrouter.CleanupCredentialPromotions(transaction.Credentials)
	err = errors.Join(err, removeCopyStage(dir, transaction))
	if err != nil {
		return err
	}
	if removeErr := os.Remove(transactionPath(dir)); removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}

func (m *Manager) recoverCopyTransaction(id string) error {
	dir := m.dir(id)
	if err := safeTree(dir); err != nil {
		return err
	}
	transaction, err := readCopyTransaction(dir, id)
	if err != nil || transaction == nil {
		return err
	}
	if transaction.Phase == copyPreparing {
		return abortCopyTransaction(dir, *transaction)
	}
	if transaction.Phase == copyCommitting {
		stageDir := filepath.Join(dir, transaction.Stage)
		if err := safeTree(stageDir); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(stageDir, "model_router.json"))
		if err != nil {
			return err
		}
		var cfg modelrouter.Configuration
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		if err := m.reserveEndpoints(id, cfg.Endpoints); err != nil {
			return err
		}
		if err := modelrouter.CommitCredentialPromotions(transaction.Credentials); err != nil {
			return err
		}
		for _, name := range copyConfigFiles {
			data, err = os.ReadFile(filepath.Join(stageDir, name))
			if err != nil {
				if name == "dialogue.yml" && os.IsNotExist(err) {
					continue
				}
				return err
			}
			if _, err := writeCopyFile(filepath.Join(dir, name), data, 0600); err != nil {
				return err
			}
		}
		transaction.Phase = copyApplied
		if _, err := writeCopyTransaction(dir, *transaction); err != nil {
			return err
		}
	} else {
		// An applied marker must be durably re-established before recovery
		// material is removed. This makes retry safe after a post-rename sync
		// failure from an earlier recovery attempt.
		if _, err := writeCopyTransaction(dir, *transaction); err != nil {
			return err
		}
	}
	if err := modelrouter.CleanupCredentialPromotions(transaction.Credentials); err != nil {
		return err
	}
	if err := removeCopyStage(dir, *transaction); err != nil {
		return err
	}
	if err := os.Remove(transactionPath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
