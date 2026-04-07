package logger

import (
	"log"

	"go.uber.org/zap"
)

var Log *zap.Logger

func InitLogger() error {
	var err error
	Log, err = zap.NewProduction()
	if err != nil {
		return err
	}
	zap.ReplaceGlobals(Log)
	log.Println("Zap logger initialized")
	return nil
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}
