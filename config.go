package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Port string
	Dir  string
}

// os.UserConfigDir()
// macos   ~/Library/Application Support/
// windows %APPDATA%\
// linux   ~/.config/

func loadConfig(filename string, userCfg Config) Config {

	// The defaults if no config file or command line data at all.
	finalCfg := Config{
		Port: "8080",
		Dir:  ".",
	}

	// Load config file. Check both in default user config location, and in current workding directory.
	// current working directory takes precedence.

	configFile, err := findFile(filename)
	if err != nil {
		fmt.Println(configFile)
	}

	if _, err := toml.DecodeFile(configFile, &finalCfg); err != nil {
		log.Printf("No 'config.toml' file. Using defaults\nErr: %s\n", err)
	}

	// Command line options override any toml settings
	if userCfg.Port != "" {
		finalCfg.Port = userCfg.Port
	}
	if userCfg.Dir != "" {
		finalCfg.Dir = userCfg.Dir
	}

	return finalCfg
}

// Check for config file.
// Look in directories where executable is stored and current working directory.
// If there is a config file in both locations, current working directory takes precedence.
func findFile(filename string) (string, error) {
	// Check current working directory
	dir, err := os.Getwd()
	if err == nil {
		configPath := filepath.Join(dir, filename)
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			return configPath, nil
		}
		log.Printf("no config file in current working directory: %s\n", configPath)
	}

	// Check executable directory for one.
	dir, err = os.UserConfigDir()
	if err == nil {
		configPath := filepath.Join(dir, "nitro", filename)
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			return configPath, nil
		}
		log.Printf("no config file in user config directory: %s\n", configPath)
	}

	return "", fmt.Errorf("no config files found")
}

// Addition for later. Have the ability to have a directory containing multiple config files. like how linux uses conf.d directories

// Load additional config files
// entries, err := os.ReadDir("config.d")
// if err == nil {
// 	var confFiles []string
// 	for _, entry := range entries {
// 		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".toml" {
// 			confFiles = append(confFiles, filepath.Join("config.d", entry.Name()))
// 		}
// 	}
// 	// Process config files in alphabetic order
// 	sort.Strings(confFiles)
// 	for _, file := range confFiles {
// 		var override config
// 		if _, err := toml.DecodeFile(file, &override); err != nil {
// 			log.Printf("Warning: Failed to parse %s: %v", file, err)
// 			continue
// 		}
// 		if override.Port != "" {
// 			tomlCfg.Port = override.Port
// 		}
// 		if override.Dir != "" {
// 			tomlCfg.Port = override.Dir
// 		}
// 	}

// }
