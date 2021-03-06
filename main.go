package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"time"
)

var (
	configPath  string
	forceUpdate bool

	// BlockCache contains all blocked domains
	BlockCache = &MemoryBlockCache{Backend: make(map[string]bool)}

	// QuestionCache contains all queries to the dns server
	QuestionCache = &MemoryQuestionCache{Backend: make([]QuestionCacheEntry, 0), Maxcount: 1000}
)

func main() {
	flag.Parse()

	if err := LoadConfig(configPath); err != nil {
		log.Fatal(err)
	}

	QuestionCache.Maxcount = Config.QuestionCacheCap

	logFile, err := LoggerInit(Config.Log)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()

	if _, err := os.Stat("lists"); os.IsNotExist(err) || forceUpdate {
		if err := Update(); err != nil {
			log.Fatal(err)
		}
	}

	if err := UpdateBlockCache(); err != nil {
		log.Fatal(err)
	}

	server := &Server{
		host:     Config.Bind,
		rTimeout: 5 * time.Second,
		wTimeout: 5 * time.Second,
	}

	server.Run()

	if err := StartAPIServer(); err != nil {
		log.Fatal(err)
	}

	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt)

forever:
	for {
		select {
		case <-sig:
			log.Printf("signal received, stopping\n")
			break forever
		}
	}
}

func init() {
	flag.StringVar(&configPath, "config", "grimd.toml", "location of the config file, if not found it will be generated (default grimd.toml)")
	flag.BoolVar(&forceUpdate, "update", false, "force an update of the blocklist database")

	runtime.GOMAXPROCS(runtime.NumCPU())
}
