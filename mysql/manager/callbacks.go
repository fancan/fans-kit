package manager

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"syscall"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// registerCallbacks 注册callback
func registerCallbacks(db *gorm.DB, nodes []*node, failureThreshold int, observeFailures bool) error {
	byPool := make(map[*sql.DB]*node, len(nodes))
	for _, n := range nodes {
		byPool[n.db] = n
	}

	primary := func(tx *gorm.DB) {
		if forcePrimary(tx.Statement.Context) {
			dbresolver.Write.ModifyStatement(tx.Statement)
		}
	}
	observe := func(tx *gorm.DB) {
		if tx.Error == nil || !isConnectivityError(tx.Error) {
			return
		}
		if pool := underlyingSQLDB(tx.Statement.ConnPool); pool != nil {
			if n := byPool[pool]; n != nil {
				n.markFailure(failureThreshold, tx.Error)
			}
		}
	}

	if err := db.Callback().Query().Before("gorm:db_resolver").Register("fans:mysql:context_primary:query", primary); err != nil {
		return err
	}
	if err := db.Callback().Row().Before("gorm:db_resolver").Register("fans:mysql:context_primary:row", primary); err != nil {
		return err
	}
	if err := db.Callback().Raw().Before("gorm:db_resolver").Register("fans:mysql:context_primary:raw", primary); err != nil {
		return err
	}

	if observeFailures {
		if err := db.Callback().Query().After("gorm:query").Register("fans:mysql:observe_replica_error:query", observe); err != nil {
			return err
		}
		if err := db.Callback().Row().After("gorm:row").Register("fans:mysql:observe_replica_error:row", observe); err != nil {
			return err
		}
		if err := db.Callback().Raw().After("gorm:raw").Register("fans:mysql:observe_replica_error:raw", observe); err != nil {
			return err
		}
	}
	return nil
}

// isConnectivityError 判断是否是连接错误
func isConnectivityError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) {
		return true
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 2002, 2003, 2006, 2013, 2055:
			return true
		}
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// underlyingSQLDB 提取sql.DB
func underlyingSQLDB(pool gorm.ConnPool) *sql.DB {
	switch p := pool.(type) {
	case *sql.DB:
		return p
	case *gorm.PreparedStmtDB:
		if db, ok := p.ConnPool.(*sql.DB); ok {
			return db
		}
	}
	return nil
}
