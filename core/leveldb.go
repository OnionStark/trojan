package core

import (
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
)

var dbPath = "/var/lib/trojan-manager"

// dbMutex 串行化本进程内的leveldb访问, 避免并发请求各自OpenFile互相抢文件锁导致读取失败
var dbMutex sync.Mutex

// openDB 带重试地打开leveldb, 缓解web服务与命令行等多进程之间偶发的文件锁竞争
func openDB() (*leveldb.DB, error) {
	var (
		db  *leveldb.DB
		err error
	)
	for i := 0; i < 10; i++ {
		if db, err = leveldb.OpenFile(dbPath, nil); err == nil {
			return db, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, err
}

// GetValue 获取leveldb值
func GetValue(key string) (string, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	db, err := openDB()
	if err != nil {
		return "", err
	}
	defer db.Close()
	result, err := db.Get([]byte(key), nil)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// SetValue 设置leveldb值
func SetValue(key string, value string) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Put([]byte(key), []byte(value), nil)
}

// DelValue 删除值
func DelValue(key string) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Delete([]byte(key), nil)
}
