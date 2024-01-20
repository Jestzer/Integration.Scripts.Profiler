package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/fatih/color"
)

func main() {
	// Colors used across the program.
	redBackground := color.New(color.BgRed).SprintFunc()
	redText := color.New(color.FgRed).SprintFunc()

	// For reading user input.
	reader := bufio.NewReader(os.Stdin)

	// Goodies.
	var defaultTMP string
	var schedulerSelected string
	var organizationSelected string
	var clusterCount int
	var clusterName string

	// # Add some code that'll load any preferences for the program.
	// # Add some code that'll allow arrow keys to be used when prompted for user input.
	// Setup for better Ctrl+C messaging. This is a channel to receive OS signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start a goroutine to listen for signals.
	go func() {

		// Wait for the signal.
		<-signalChan

		// Handle the signal by exiting the program.
		fmt.Println(redBackground("\nExiting from user input..."))
		os.Exit(0)
	}()

	// Figure out your OS.
	switch userOS := runtime.GOOS; userOS {
	case "darwin":
		defaultTMP = "/tmp"
	case "windows":
		defaultTMP = os.Getenv("TMP")
	case "linux":
		defaultTMP = "/tmp"
	default:
		defaultTMP = "unknown"
		fmt.Println(redText("Your operating system is unrecognized. Exiting."))
		os.Exit(0)
	}

	for {
		fmt.Print("Enter the organization's name.\n")
		organizationSelected, _ = reader.ReadString('\n')
		organizationSelected = strings.TrimSpace(organizationSelected)

		if organizationSelected == "" {
			fmt.Print("Invalid entry. ")
			continue
		} else {
			break
		}
	}

	for {
		fmt.Print("Enter the number of clusters you'd like to make scripts for. 1-6 are accepted. Entering nothing will select 1.\n")
		fmt.Scan(&clusterCount)
		break
	}

	for {
		fmt.Print("Enter the cluster's name.\n")
		clusterName, _ = reader.ReadString('\n')
		clusterName = strings.TrimSpace(clusterName)

		if clusterName == "" {
			fmt.Print("Invalid entry. ")
			continue
		} else {
			break
		}
	}

	// waaaahhhh it's too difficult to just say "while".
	for {
		fmt.Print("Select the scheduler you'd like to use by entering its corresponding number. Entering nothing will select Slurm.\n")
		fmt.Print("[1 Slurm] [2 PBS] [3 LSF] [4 Grid Engine] [5 HTCondor] [6 AWS] [7 Kubernetes]\n")
		schedulerSelected, _ = reader.ReadString('\n')
		schedulerSelected = strings.TrimSpace(schedulerSelected)
		break
		// Shut the hell up Go.
		fmt.Print(defaultTMP)
	}

}

// Function to download a file from a given URL and save it to the specified path.
func downloadFile(url string, filePath string) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return err
	}

	return nil
}

// Function to unzip integration scripts.
func unzipFile(src, dest string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		path := filepath.Join(dest, file.Name)

		// Reconstruct the file path on Windows to ensure proper subdirectories are created.
		// Don't know why other OSes don't need this.
		if runtime.GOOS == "windows" {
			path = filepath.Join(dest, file.Name)
			path = filepath.FromSlash(path)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(path, file.Mode())
			continue
		}

		err := os.MkdirAll(filepath.Dir(path), 0755)
		if err != nil {
			return err
		}

		fileReader, err := file.Open()
		if err != nil {
			return err
		}
		defer fileReader.Close()

		targetFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}
		defer targetFile.Close()

		_, err = io.Copy(targetFile, fileReader)
		if err != nil {
			return err
		}
	}

	return nil
}
