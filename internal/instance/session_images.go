package instance

import (
	"context"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// SessionImageQuotaBytes caps the durable conversation images kept per instance;
// the least recently viewed images are evicted first.
const SessionImageQuotaBytes int64 = 512 << 20

const sessionImageIOTimeout = 10 * time.Second

// sqlSessionImages persists conversation preview images in the shared database.
type sqlSessionImages struct {
	db         *storage.DB
	instanceID string
	quota      int64
}

func (s sqlSessionImages) PutSessionImage(key, contentType string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionImageIOTimeout)
	defer cancel()
	return s.db.PutSessionImage(ctx, s.instanceID, key, contentType, data, s.quota)
}

func (s sqlSessionImages) GetSessionImage(key string) (string, []byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionImageIOTimeout)
	defer cancel()
	image, ok, err := s.db.GetSessionImage(ctx, s.instanceID, key)
	if err != nil || !ok {
		return "", nil, false
	}
	return image.ContentType, image.Data, true
}

func (s sqlSessionImages) HasSessionImage(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), sessionImageIOTimeout)
	defer cancel()
	ok, err := s.db.HasSessionImage(ctx, s.instanceID, key)
	return err == nil && ok
}
