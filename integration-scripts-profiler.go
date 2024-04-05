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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/fatih/color"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/xanzy/go-gitlab"
)

type FolderCompleter struct {
	Folders []string
}

// This function needs to be placed before the main function IIRC.
func (f *FolderCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	prefix := string(line[:pos]) // Ensure we're only considering the part of the line up to the cursor.
	for _, folder := range f.Folders {
		if strings.HasPrefix(folder, prefix) {
			// Append only the remaining part of the folder name beyond the prefix.
			remainingPart := folder[len(prefix):]
			newLine = append(newLine, []rune(remainingPart))
		}
	}
	length = len(prefix)
	return
}

// Cross-function variables.
var (
	accessToken          string
	organizationSelected string
	gitGroupID           int
	gitRepoAPIURL        string
	gitGroupName         string
	gitUsername          string
	gitEmailAddress      string
	needToCreateGitRepo  bool
	organizationPath     string
)

func main() {
	// To handle keyboard input better.
	rl, err := readline.New("> ")
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	// Colors used across the program.
	redBackground := color.New(color.BgRed).SprintFunc()
	redText := color.New(color.FgRed).SprintFunc()

	// Goodies.
	var input string
	var scriptsPath string
	var downloadScriptsOnLanuch bool = true
	var useCaseNumber bool = true
	var caseNumber int
	var gitRepoPath string
	var schedulerSelected string
	var organizationAbbreviation string
	var organizationContact string
	var customMPIInput string
	var customMPI bool
	var clusterCount int
	var clusterName string
	var submissionType string
	var numberOfWorkers int
	var hasSharedFileSystem bool
	var clusterMatlabRoot string
	var clusterHostname string
	var remoteJobStorageLocation string

	// Setup for better Ctrl+C messaging. This is a channel to receive OS signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start a Goroutine to listen for signals.
	go func() {

		// Wait for the signal.
		<-signalChan

		// Handle the signal by exiting the program and reporting it as so.
		fmt.Print(redBackground("\nExiting from user input..."))
		os.Exit(0)
	}()

	// Regexp compile used for detecting things with numbers and letters.
	lettersAndNumbersPattern, err := regexp.Compile(`^[^a-zA-Z0-9]+$`)
	if err != nil {
		fmt.Println(redText("Error compiling regex pattern: ", err, " Exiting."))
		os.Exit(1)
	}

	lettersPattern, err := regexp.Compile(`^[^a-zA-Z]+$`)
	if err != nil {
		fmt.Println(redText("Error compiling regex pattern: ", err, " Exiting."))
		os.Exit(1)
	}

	// Determine your OS.
	switch userOS := runtime.GOOS; userOS {
	case "darwin":
		scriptsPath = "/tmp"
	case "windows":
		scriptsPath = os.Getenv("TMP")
	case "linux":
		scriptsPath = "/tmp"
	default:
		scriptsPath = "unknown"
		fmt.Print(redText("\nYour operating system is unrecognized. Exiting."))
		os.Exit(1)
	}

	// Determine any user-defined settings.
	currentDir, err := os.Getwd() // Get the current working directory.
	if err != nil {
		fmt.Print(redText("\nError getting current working directory while looking for user settings : ", err, " Default settings will be used instead."))
		return
	} else {
		settingsPath := filepath.Join(currentDir, "settings.txt")

		// Check if the settings file exists.
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			// No settings file found.
			return
		} else if err != nil {
			fmt.Print(redText("\nError checking for user settings: ", err, " Default settings will be used instead."))
		} else {
			fmt.Print("\nCustom settings found!")
			file, err := os.Open(settingsPath)
			if err != nil {
				fmt.Print(redText("\nError opening settings file: ", err, " Default settings will be used instead."))
				return
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)

			for scanner.Scan() {
				line := scanner.Text()

				if !strings.HasPrefix(line, "#") {
					if strings.HasPrefix(strings.ToLower(line), "downloadscriptsonlaunch") {
						if strings.Contains(strings.ToLower(line), "false") {
							downloadScriptsOnLanuch = false
							fmt.Print("\nA new set of integration scripts will not be downloaded per your settings.")
						}

					} else if strings.HasPrefix(line, "scriptsPath =") || strings.HasPrefix(line, "scriptsPath=") {
						scriptsPath = strings.TrimPrefix(line, "scriptsPath =")
						scriptsPath = strings.TrimPrefix(scriptsPath, "scriptsPath=")
						scriptsPath = strings.TrimSpace(scriptsPath)
						scriptsPath = strings.Trim(scriptsPath, "\"")

						_, err := os.Stat(scriptsPath) // Do you actually exist? Does anything actually exist, man?
						if err != nil {
							fmt.Print(redText("\nThe custom scripts path you've specified, \"", scriptsPath, " does not exist. Please adjust your settings accordingly."))
							os.Exit(1)
						}

						if !downloadScriptsOnLanuch {
							schedulers := []string{"slurm", "pbs", "lsf", "gridengine", "htcondor", "awsbatch", "kubernetes"}
							for _, scheduler := range schedulers {
								schedulerDirectoryName := "matlab-parallel-" + scheduler + "-plugin-main"
								schedulerPath := filepath.Join(scriptsPath, schedulerDirectoryName)
								if _, err := os.Stat(schedulerPath); err != nil {
									fmt.Printf(redText("\nThe path you've specified is missing the needed integration scripts folder \"%s\".\n"), schedulerDirectoryName)
									os.Exit(1)
								}
							}
						}

						fmt.Print("\nA custom integration scripts download path has been set to ", scriptsPath)

					} else if strings.HasPrefix(line, "accessToken =") || strings.HasPrefix(line, "accessToken=") {
						accessToken = strings.TrimPrefix(line, "accessToken =")
						accessToken = strings.TrimPrefix(accessToken, "accessToken=")
						accessToken = strings.TrimSpace(accessToken)
						accessToken = strings.Trim(accessToken, "\"")
						fmt.Print("\nYour access token has been set to ", accessToken)
					} else if strings.HasPrefix(line, "gitGroupID =") || strings.HasPrefix(line, "gitGroupID=") {
						gitGroupIDString := strings.TrimPrefix(line, "gitGroupID =")
						gitGroupIDString = strings.TrimPrefix(gitGroupIDString, "gitGroupID=")
						gitGroupIDString = strings.TrimSpace(gitGroupIDString)
						gitGroupIDString = strings.Trim(gitGroupIDString, "\"")

						if _, err := strconv.Atoi(gitGroupIDString); err == nil {
							gitGroupID, _ = strconv.Atoi(gitGroupIDString)
						}

						fmt.Print("\nYour Git group ID has been set to ", gitGroupID)
					} else if strings.HasPrefix(line, "gitRepoPath =") || strings.HasPrefix(line, "gitRepoPath=") {
						gitRepoPath = strings.TrimPrefix(line, "gitRepoPath =")
						gitRepoPath = strings.TrimPrefix(gitRepoPath, "gitRepoPath=")
						gitRepoPath = strings.TrimSpace(gitRepoPath)
						gitRepoPath = strings.Trim(gitRepoPath, "\"")

						// Check if the path exists.
						if _, err := os.Stat(gitRepoPath); os.IsNotExist(err) {
							fmt.Print("\nThe specified Git repo path does not exist: ", gitRepoPath, ". It will not be used.")
							gitRepoPath = ""
						} else {
							fmt.Print("\nYour Git Repo path has been set to ", gitRepoPath)
						}
					} else if strings.HasPrefix(line, "gitRepoAPIURL =") || strings.HasPrefix(line, "gitRepoAPIURL=") {
						gitRepoAPIURL = strings.TrimPrefix(line, "gitRepoAPIURL =")
						gitRepoAPIURL = strings.TrimPrefix(gitRepoAPIURL, "gitRepoAPIURL=")
						gitRepoAPIURL = strings.TrimSpace(gitRepoAPIURL)
						gitRepoAPIURL = strings.Trim(gitRepoAPIURL, "\"")

						// We want the URL to end with "projects/"" for later Git repo usage.
						if strings.HasSuffix(gitRepoAPIURL, "projects") {
							gitRepoAPIURL += "/"
						} else if strings.HasSuffix(gitRepoAPIURL, "projects/") {
							// Do nothing.
						} else if strings.HasSuffix(gitRepoAPIURL, "/") {
							gitRepoAPIURL = gitRepoAPIURL[:len(gitRepoAPIURL)-1]
							gitRepoAPIURL += "/projects"
						}

						fmt.Print("\nYour Git API URL has been set to ", gitRepoAPIURL)
					} else if strings.HasPrefix(line, "gitGroupName =") || strings.HasPrefix(line, "gitGroupName=") {
						gitGroupName = strings.TrimPrefix(line, "gitGroupName =")
						gitGroupName = strings.TrimPrefix(gitGroupName, "gitGroupName=")
						gitGroupName = strings.TrimSpace(gitGroupName)
						gitGroupName = strings.Trim(gitGroupName, "\"")
						fmt.Print("\nYour Git group name has been set to ", gitGroupName)
					} else if strings.HasPrefix(line, "gitUsername =") || strings.HasPrefix(line, "gitUsername=") {
						gitUsername = strings.TrimPrefix(line, "gitUsername =")
						gitUsername = strings.TrimPrefix(gitUsername, "gitUsername=")
						gitUsername = strings.TrimSpace(gitUsername)
						gitUsername = strings.Trim(gitUsername, "\"")
						fmt.Print("\nYour Git repo username has been set to ", gitUsername)
					} else if strings.HasPrefix(line, "gitEmailAddress =") || strings.HasPrefix(line, "gitEmailAddress=") {
						gitEmailAddress = strings.TrimPrefix(line, "gitEmailAddress =")
						gitEmailAddress = strings.TrimPrefix(gitEmailAddress, "gitEmailAddress=")
						gitEmailAddress = strings.TrimSpace(gitEmailAddress)
						gitEmailAddress = strings.Trim(gitEmailAddress, "\"")
						fmt.Print("\nYour Git repo email address has been set to ", gitEmailAddress)
					} else if strings.HasPrefix(strings.ToLower(line), "usecasenumber") {
						if strings.Contains(strings.ToLower(line), "false") {
							useCaseNumber = false
							fmt.Print("\nPer your settings, you will not be prompted to fill in a Case Number.")
						}
					} else {
						fmt.Print(redText("\nUnrecognized setting detected. The line in question has this content: ", line))
					}
				}
			}

			if err := scanner.Err(); err != nil {
				fmt.Print(redText("\nError reading settings file: ", err, " Default settings will be used instead."))
			}
		}
	}

	if downloadScriptsOnLanuch {
		fmt.Print("\nBeginning download of integration scripts. Please wait.")

		var scriptsURLs = map[string]string{
			"https://codeload.github.com/mathworks/matlab-parallel-slurm-plugin/zip/refs/heads/main":      "slurm.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-pbs-plugin/zip/refs/heads/main":        "pbs.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-lsf-plugin/zip/refs/heads/main":        "lsf.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-htcondor-plugin/zip/refs/heads/main":   "htcondor.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-gridengine-plugin/zip/refs/heads/main": "gridengine.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-awsbatch-plugin/zip/refs/heads/main":   "awsbatch.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-kubernetes-plugin/zip/refs/heads/main": "kubernetes.zip",
		}

		for url, zipArchive := range scriptsURLs {
			zipArchivePath := filepath.Join(scriptsPath, zipArchive)
			err := downloadFile(url, zipArchivePath)
			if err != nil {
				fmt.Print(redText("\nFailed to download the integration scripts: ", err))
				continue
			}

			// Extract ZIP archives.
			schedulerName := strings.TrimSuffix(zipArchive, ".zip")
			unzipPath := filepath.Join(scriptsPath, schedulerName)

			// Check if the integration scripts directory already exists. Delete it if it is.
			if _, err := os.Stat(unzipPath); err == nil {

				err := os.RemoveAll(unzipPath)
				if err != nil {
					fmt.Print(redText("\nFailed to delete the existing integration scripts directory: ", err))
					continue
				}
			}

			err = unzipFile(zipArchivePath, scriptsPath)
			if err != nil {
				fmt.Print(redText("\nFailed to extract integration scripts: ", err))
				os.Exit(1)
			}

			if strings.Contains(zipArchivePath, "kubernetes.zip") {
				fmt.Print("\nLatest integration scripts downloaded and extracted successfully!")
			}
		}
	} else {
		fmt.Print("\nIntegration scripts download skipped per user's settings.")
	}

	// List existing engagements and setup auto-completion.
	var engagementFolders []string
	if gitRepoPath != "" {
		customerEngagementsPath := filepath.Join(gitRepoPath, "Customer-Engagements")
		if _, err := os.Stat(customerEngagementsPath); !os.IsNotExist(err) {
			files, err := os.ReadDir(customerEngagementsPath)
			if err != nil {
				fmt.Print(redText("\nError reading directory: ", err))
			} else {
				fmt.Print("\n\nExisting engagements found:\n\n")
				for _, f := range files {

					// Don't list "hidden" folders.
					if strings.HasPrefix(f.Name(), ".") {
						continue
					}

					if f.IsDir() {
						engagementFolders = append(engagementFolders, f.Name())
						fmt.Println("-", f.Name())
					}
				}
			}
		}
	}

	// Setup auto-completer.
	completer := &FolderCompleter{Folders: engagementFolders}
	rl.Config.AutoComplete = completer

	for {
		fmt.Print("\nEnter the organization's name.\n")
		organizationSelected, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line: ", err))
				continue
			}
			return
		}
		organizationSelected = strings.TrimSpace(organizationSelected)

		if organizationSelected == "" {
			fmt.Print(redText("Invalid entry. "))
			continue
		} else {
			break
		}
	}

	// Now that we know what the organization's name is, define its path.
	organizationPath = filepath.Join(gitRepoPath, "Customer-Engagements", organizationSelected)
	//gitURLToCheck := gitGroupName + "/" + organizationSelected

	// And we can check if the remote repo exists!
	exists, err := CheckIfGitLabProjectExists(organizationSelected, accessToken)
	if err != nil {
		fmt.Println("Error checking project existence: ", err)
		return
	}

	if exists {
		fmt.Println("The project exists.")
		needToCreateGitRepo = false
	} else {
		fmt.Println("The project does not exist.")
		needToCreateGitRepo = true
	}

	// # Add some code that'll check to see if the abbreviation has already been set in the remote git repo.
	for {
		fmt.Print("\nEnter the organization's abrreviation. If it's unknown, leave it empty.\n")
		organizationAbbreviation, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line: ", err))
				continue
			}
			return
		}
		organizationAbbreviation = strings.TrimSpace(organizationAbbreviation)

		if organizationAbbreviation == "" {
			break
		} else if lettersPattern.MatchString(organizationAbbreviation) && organizationAbbreviation != "" {
			fmt.Print(redText("\nInvalid input. You may only use letters in the abbreviation and at least 1 letter is required.\n"))
			continue
		} else {
			break
		}
	}

	if gitRepoPath != "" {

		// List existing contacts and setup auto-completion.
		var contactFolders []string
		if gitRepoPath != "" {
			if _, err := os.Stat(organizationPath); !os.IsNotExist(err) {
				files, err := os.ReadDir(organizationPath)
				if err != nil {
					fmt.Print(redText("\nError reading directory: ", err))
				} else {
					fmt.Print("\n\nExisting contacts found:\n\n") // # Add some code to not list anything if no contacts are found.
					for _, f := range files {

						// We don't want your .git folders listed, thanks.
						if strings.HasPrefix(f.Name(), ".") {
							continue
						}

						if f.IsDir() {
							contactFolders = append(contactFolders, f.Name())
							fmt.Println("-", f.Name())
						}
					}
				}
			}
		}

		// Setup auto-completer.
		completer := &FolderCompleter{Folders: contactFolders}
		rl.Config.AutoComplete = completer

		for {
			fmt.Print("\nEnter the organization's contact name. If it's unknown, leave it empty and it will populate as \"first-last\".\n")
			organizationContact, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}

			organizationContact = strings.TrimSpace(organizationContact)

			if organizationContact == "" {
				organizationContact = "first-last"
				break
			} else if lettersPattern.MatchString(organizationContact) && organizationContact != "" {
				fmt.Print(redText("\nInvalid input. You may only use letters in the contact name and at least 1 letter is required.\n"))
				continue
			} else {
				break
			}
		}
	}
	if useCaseNumber {
		for {
			fmt.Print("Enter the Salesforce Case Number associated with these scripts. Press Enter to skip.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			// Don't accept anything other than numbers and blank input.
			if input == "" {
				useCaseNumber = false
				break
			} else if _, err := strconv.Atoi(input); err == nil && input != "" { // # Add som code to do something with the potential error message.
				caseNumber, _ = strconv.Atoi(input)
				if caseNumber < 01000000 {
					fmt.Print(redText("Are you sure that's the right Case Number? It seems a bit too small.\n"))
					continue
				} else if caseNumber > 20000000 {
					fmt.Print(redText("Are you sure that's the right Case Number? It seems a bit too large.\n"))
					continue
				}
				break
			} else {
				fmt.Print(redText("Invalid entry. "))
				continue
			}
		}
	}

	for {
		fmt.Print("Enter the number of clusters you'd like to make scripts for. Entering nothing will select 1.\n")
		input, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line: ", err))
				continue
			}
			return
		}
		input = strings.TrimSpace(input)

		// Don't accept anything other than numbers and blank input.
		if input == "" {
			clusterCount = 1
			break
		}

		if _, err := strconv.Atoi(input); err == nil {
			clusterCount, _ = strconv.Atoi(input)
			if clusterCount < 1 {
				fmt.Print(redText("Invalid entry. You've selected zero or less clusters to create scripts for.\n"))
				continue
			}
			break
		} else {
			fmt.Print(redText("Invalid entry. Please enter an integer greater than zero.\n"))
			continue
		}
	}

	// Loop cluster creation for as many times as you specified.
	for i := 1; i <= clusterCount; i++ {
		for {
			fmt.Print("Enter cluster #", i, "'s name. Entering nothing will use \"HPC\"\n")
			clusterName, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}
			clusterName = strings.TrimSpace(clusterName)

			if clusterName == "" {
				clusterName = "HPC"
				break
			} else if lettersAndNumbersPattern.MatchString(clusterName) && clusterName != "" {
				fmt.Print(redText("Invalid input. You must include at least 1 letter or number in the cluster's name.\n"))
				continue
			} else {
				break
			}
		}

		// Map numbers to actual scheduler names.
		schedulerMap := map[int]string{
			1: "Slurm",
			2: "PBS",
			3: "LSF",
			4: "Grid Engine",
			5: "HTCondor",
			6: "AWS",
			7: "Kubernetes",
		}

		// waaaahhhh it's too difficult to just say "while".
		for {
			fmt.Print("Select the scheduler you'd like to use by entering its corresponding number. Entering nothing will select Slurm.\n")
			fmt.Print("[1 Slurm] [2 PBS] [3 LSF] [4 Grid Engine] [5 HTCondor] [6 AWS] [7 Kubernetes]\n")
			schedulerSelected, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}

			if schedulerSelected == "" {
				schedulerSelected = "Slurm"
				break
			}

			// Parse for an integer to make for some prettier code later.
			var schedulerNumberSelected int
			parsedInt, err := strconv.Atoi(schedulerSelected)
			if err != nil {
				fmt.Print(redText("\nYou did not enter a number. Enter a number to select a scheduler.\n"))
				continue
			} else {
				schedulerNumberSelected = parsedInt
			}

			if schedulerNumberSelected < 1 || schedulerNumberSelected > 7 {
				fmt.Print(redText("\nYou selected an invalid number. You must select a number between 1-7.\n"))
				schedulerSelected = schedulerMap[schedulerNumberSelected]
				continue
			} else {
				break
			}
		}

		for {
			fmt.Print("Would you like to use include the custom MPI file? (y/n) Entering nothing will not include it.\n")
			customMPIInput, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}

			strings.TrimSpace(strings.ToLower(customMPIInput))

			if customMPIInput == "" {
				customMPI = false
				break
			} else if customMPIInput == "y" || customMPIInput == "yes" {
				customMPI = true
				break
			} else if customMPIInput == "n" || customMPIInput == "no" {
				customMPI = false
				break
			} else {
				fmt.Print(redText("\nInvalid input. You must enter one of the following: \"y\" or \"n\".\n"))
				continue
			}
		}

		for {
			fmt.Print("Select the submissions types you'd like to include by entering its corresponding number. Entering nothing will select both.\n")
			fmt.Print("[1 Desktop] [2 Cluster] [3 Both]\n")
			submissionType, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}
			submissionType = strings.TrimSpace(strings.ToLower(submissionType))

			if submissionType == "" {
				submissionType = "both"
				break
			} else if submissionType == "1" || submissionType == "2" || submissionType == "3" {
				switch submissionType {
				case "1":
					submissionType = "desktop"
				case "2":
					submissionType = "cluster"
				case "3":
					submissionType = "both"
				}
				break
			} else if submissionType == "cluster" || submissionType == "desktop" || submissionType == "both" {
				break
			} else {
				fmt.Print(redText("Invalid entry. Enter a number between 1-3 to select a submission type.\n"))
				continue
			}
		}

		// # Add some code that'll ask the user if they want to include the remote submission scripts.

		for {
			fmt.Print("Enter the number of workers available on the cluster's license. Entering nothing will select 100,000.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line: ", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			if input == "" {
				numberOfWorkers = 100000
				break
				fmt.Print(numberOfWorkers) // Again, Go, shut up.
			}

			// Don't accept anything other than numbers.
			if _, err := strconv.Atoi(input); err == nil {
				numberOfWorkers, _ = strconv.Atoi(input)
				if numberOfWorkers < 1 {
					fmt.Print(redText("Invalid entry. You've selected zero or less workers.\n"))
					continue
				} else if numberOfWorkers > 100000 {
					fmt.Print(redText("Invalid entry. You've selected more than 100000 workers, which is not offered on any license.\n"))
					continue
				}
				break
			} else {
				fmt.Print(redText("Invalid entry. You've likely included a character other than a number.\n"))
				continue
			}
		}
		if submissionType == "desktop" || submissionType == "both" {
			for {
				fmt.Print("Does the client have a shared filesystem with the cluster? (y/n)\n")
				input, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line: ", err))
						continue
					}
					return
				}
				input = strings.TrimSpace(strings.ToLower(input))

				if input == "y" || input == "yes" {
					hasSharedFileSystem = true
					break
					fmt.Print(hasSharedFileSystem) // Again, shut up, Go. It'll be used at some point, I promise.
				} else if input == "n" || input == "no" {
					hasSharedFileSystem = false
					break
				} else {
					fmt.Print(redText("Invalid entry.\n"))
					continue
				}
			}

			for {
				fmt.Print("What is the full filepath of MATLAB on the cluster? (ex: /usr/local/MATLAB/R2023b)\n")
				clusterMatlabRoot, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line: ", err))
						continue
					}
					return
				}
				clusterMatlabRoot = strings.TrimSpace(clusterMatlabRoot)

				if strings.Contains(clusterMatlabRoot, "/") || strings.Contains(clusterMatlabRoot, "\\") {
					break
				} else {
					fmt.Print(redText("Invalid filepath. "))
					continue
				}
			}

			for {
				fmt.Print("What is the hostname, FQDN, or IP address used to SSH to the cluster?\n")
				clusterHostname, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line: ", err))
						continue
					}
					return
				}
				clusterHostname = strings.TrimSpace(clusterHostname)

				if clusterHostname == "" {
					fmt.Print(redText("Invalid entry. "))
					continue
				} else {
					break
				}
			}

			for {
				fmt.Print("Where will remote job storage location be on the cluster? Entering nothing will select /home/$USER/.matlab/generic_cluster_jobs/" + clusterName + "/$Host\n")
				remoteJobStorageLocation, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line: ", err))
						continue
					}
					return
				}
				remoteJobStorageLocation = strings.TrimSpace(remoteJobStorageLocation)

				if strings.Contains(remoteJobStorageLocation, "/") || strings.Contains(clusterMatlabRoot, "\\") {
					break
				} else if remoteJobStorageLocation == "" {
					remoteJobStorageLocation = "/home/$USER/.matlab/generic_cluster_jobs/" + clusterName + "/$HOST"
					break
				} else {
					fmt.Print(redText("Invalid filepath. "))
					continue
				}
			}
		}
		fmt.Print("Creating integration scripts for cluster #", i, "...\n")

		// Create the organization's directory, if it's not already in existence.
		err := ensureDir(organizationPath)
		if err != nil {
			fmt.Printf("Error creating directory: %s\n", err)
		} else {
			fmt.Println("Organization directory created!")
		}

		// This is where Big Things Part 1(tm) will happen.
		// These are just here for now to make Go shut the hell up.
		if useCaseNumber {
			fmt.Print("Case number: ", caseNumber, "\n")
		}
		if customMPI {
			fmt.Print("you did it. custom mpi. yipee.")
		}
		if remoteJobStorageLocation != "" {
			fmt.Print("omg remotejobstl: ", remoteJobStorageLocation, "\n")
		}
		fmt.Print("Finished script creation for cluster #", i, "!\n")
	}
	fmt.Print("Submitting to your remote Git repo...\n")
	// This is where Big Things Part 2(tm) will happen (sort of.)

	// Create testing file, for now.
	testFilePath := filepath.Join(organizationPath, "testing")

	file, err := os.Create(testFilePath)
	if err != nil {
		fmt.Println("Error creating file: ", err)
		return
	}
	defer file.Close()

	fmt.Println("Blank file 'testing' created successfully at: ", testFilePath)

	// Create the local repo, if needed.
	organizationDotGitFolder := filepath.Join(organizationPath, ".git")

	if _, err := os.Stat(organizationDotGitFolder); os.IsNotExist(err) {
		if err := createLocalGitRepo(organizationPath); err != nil {
			fmt.Println("Error creating local Git repo: ", err)
			os.Exit(1)
		}
	} else if err != nil {
		fmt.Print(redText("\nError checking if .git directory exists: ", err))
		return
	} else {
		// The .git directory exists, no action needed
		fmt.Println(".git directory already exists.")
	}

	// Create the repo on GitLab, if needed.
	if needToCreateGitRepo {
		projectURL, err := createGitLabRepo(organizationSelected, accessToken, gitRepoAPIURL, gitGroupID)
		if err != nil {
			fmt.Print(redText("\nError creating GitLab project: ", err))
			return
		}
		fmt.Println("GitLab project created: ", projectURL)
	}

	// Commit the changes made and push them to the remote repo.
	if err := commitAndPush(organizationPath, organizationSelected, gitUsername, accessToken); err != nil {
		fmt.Print(redText("\nError committing or pushing: ", err))
		return
	}

	fmt.Println("Pushed to GitLab successfully.")
	fmt.Print("Finished!\n")
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

		// Reconstruct the file path on Windows to ensure proper subdirectories are created. Don't know why other OSes don't need this.
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

// If the directory doesn't already exist, make it!
func ensureDir(path string) error {
	err := os.MkdirAll(path, 0755)
	if err != nil {
		return err
	}
	return nil
}

func CheckIfGitLabProjectExists(organizationSelected, accessToken string) (bool, error) {

	urlToCheck := gitRepoAPIURL + gitGroupName + "%2F" + organizationSelected

	// Create a new request
	req, err := http.NewRequest("GET", urlToCheck, nil)
	if err != nil {
		return false, err
	}

	// Add the private token to the request headers for authentication.
	req.Header.Add("PRIVATE-TOKEN", accessToken)

	// Execute the request.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return false, nil
	}

	// A 200 status code means the project exists.
	if resp.StatusCode == 200 {
		return true, nil
	}

	// For other status codes, read the response body and return it as an error.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	return false, fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(body))
}

func createLocalGitRepo(folderPath string) error {
	_, err := git.PlainInit(folderPath, false)
	return err
}

func createGitLabRepo(projectName, accessToken, gitRepoAPIURL string, namespaceID int) (string, error) {
	gitRepoAPIWithNoProjectURL := gitRepoAPIURL[:len(gitRepoAPIURL)-9]
	git, err := gitlab.NewClient(accessToken, gitlab.WithBaseURL(gitRepoAPIWithNoProjectURL))
	if err != nil {
		return "", err
	}

	project, _, err := git.Projects.CreateProject(&gitlab.CreateProjectOptions{
		Name:        &projectName,
		NamespaceID: gitlab.Ptr(namespaceID), // Specify the namespace ID here
	})

	if err != nil {
		return "", err
	}

	return project.WebURL, nil
}

func commitAndPush(folderPath, projectName, gitUsername, accessToken string) error {
	r, err := git.PlainOpen(folderPath)
	if err != nil {
		return err
	}

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "main",
		// Note to future self: check to make sure this URL is coming out correctly.
		// Current error: invalid pkt-len found
		URLs: []string{fmt.Sprintf("https://insidelabs-git.mathworks.com/%s/%s.git", gitUsername, projectName)},
	})
	if err != nil && !strings.Contains(err.Error(), "remote already exists") {
		return err
	}

	w, err := r.Worktree()
	if err != nil {
		return err
	}

	// Stage all changes to the folder.
	err = w.AddWithOptions(&git.AddOptions{All: true})
	if err != nil {
		return err
	}

	_, err = w.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  gitUsername,
			Email: gitEmailAddress,
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	err = r.Push(&git.PushOptions{
		RemoteName: "main",
		Auth: &githttp.BasicAuth{
			Username: gitUsername,
			Password: accessToken,
		},
	})
	if err != nil {
		return err
	}

	return nil
}
