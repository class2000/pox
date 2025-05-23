package Launcher

import (
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
)

type BaseArg struct {
	LogLevel      zerolog.Level
	LogFile       string
	interruptChan chan os.Signal
	f             *os.File
	VariableEnv   bool
}

func (arg *BaseArg) init() {
	zerolog.SetGlobalLevel(arg.LogLevel)
	if arg.LogFile == "" {
		//log.SetFormatter(&log.JSONFormatter{})
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		var err error
		arg.f, err = os.OpenFile(arg.LogFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			log.Panic().Msgf("No such file for log %s", arg.LogFile)
		}
		log.Logger = log.Output(arg.f)
	}
	//zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	arg.interruptChan = make(chan os.Signal, 1)
	signal.Notify(arg.interruptChan, os.Interrupt)
}

func (arg *BaseArg) close() {
	close(arg.interruptChan)
	if arg.LogFile != "" {
		defer func() {
			err := arg.f.Close()
			if err != nil {
				fmt.Println("Error closing the log file : ", err.Error())
			}
			log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
		}()
	}
}

func (baseArg BaseArg) NewBaseArg(logLevel string) BaseArg {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	var err error
	baseArg.LogLevel, err = zerolog.ParseLevel(logLevel)

	if err != nil {
		fmt.Println("debug in {trace|debug|info|warn|error|fatal|panic}")
		os.Exit(1)
	}
	return baseArg
}
