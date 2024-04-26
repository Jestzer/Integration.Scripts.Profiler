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
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/xanzy/go-gitlab"
)

type FolderCompleter struct {
	Folders []string
}

// Used for copying files later on.
type fileCopyTask struct {
	sourceFile          string
	destinationFileName string
	destinationBasePath string
	isDirectory         bool
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
	accessToken                  string
	organizationSelected         string
	gitExistingRepoCommitMessage string
	gitGroupID                   int
	gitGroupName                 string
	gitRepoAPIURL                string
	gitUsername                  string
	gitEmailAddress              string
	needToCreateRemoteGitRepo    bool
	organizationPath             string
	organizationAbbreviation     string
	releaseNumber                string
	team                         string
	submitToRemoteRepo           bool
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
	var caseNumber int
	var clusterCount int
	var clusterHostname string
	var clusterMatlabRoot string
	var clusterName string
	var customMPI bool = false
	var customMPIInput string
	var downloadScriptsOnLanuch bool = true
	var gitRepoPath string
	var includeRemoteConfigFiles bool = false
	var input string
	var numberOfWorkers int
	var organizationContact string
	var organizationContactPath string
	var profileName string
	var schedulerSelected string
	var scriptsPath string
	var submissionType string
	var tmpFolder string
	var tmpOrganizationContactPath string

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
		fmt.Print(redText("\nError compiling regex lettersAndNumbersPattern: ", err))
		os.Exit(1)
	}

	lettersPattern, err := regexp.Compile(`^[^a-zA-Z]+$`)
	if err != nil {
		fmt.Print(redText("\nError compiling regex lettersPattern: ", err))
		os.Exit(1)
	}

	removeBannedSymbols, err := regexp.Compile("[^a-zA-Z0-9._-]+")
	if err != nil {
		fmt.Print(redText("\nError compiling regex removeBannedSymbols:", err))
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

	// We want to remember this, even if you decide to change your scriptsPath.
	tmpFolder = scriptsPath

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
					} else if strings.HasPrefix(line, "gitExistingRepoCommitMessage =") || strings.HasPrefix(line, "gitExistingRepoCommitMessage=") {
						gitExistingRepoCommitMessage = strings.TrimPrefix(line, "gitExistingRepoCommitMessage =")
						gitExistingRepoCommitMessage = strings.TrimPrefix(gitExistingRepoCommitMessage, "gitExistingRepoCommitMessage=")
						gitExistingRepoCommitMessage = strings.TrimSpace(gitExistingRepoCommitMessage)
						gitExistingRepoCommitMessage = strings.Trim(gitExistingRepoCommitMessage, "\"")
						fmt.Print("\nYour existing Git repo commit message has been set to \"", gitExistingRepoCommitMessage, "\"")
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
					} else if strings.HasPrefix(line, "releaseNumber =") || strings.HasPrefix(line, "releaseNumber=") {
						releaseNumber = strings.TrimPrefix(line, "releaseNumber =")
						releaseNumber = strings.TrimPrefix(releaseNumber, "releaseNumber=")
						releaseNumber = strings.TrimSpace(releaseNumber)
						releaseNumber = strings.Trim(releaseNumber, "\"")
						fmt.Print("\nThe release number has been set to ", releaseNumber)
					} else if strings.HasPrefix(strings.ToLower(line), "team") {
						if strings.Contains(strings.ToLower(line), "install") {
							team = "install"
							fmt.Print("\nYour team has been set to Install.")
						} else if strings.Contains(strings.ToLower(line), "parallel") {
							team = "parallel"
							fmt.Print("\nYour team has been set to Parallel Pilot.")
						} else {
							fmt.Print(redText("\nYou selected a team other than Install or Parallel Pilot team in your settings Please correct this."))
							os.Exit(1)
						}
					} else if strings.HasPrefix(strings.ToLower(line), "submittoremoterepo") {
						if strings.Contains(strings.ToLower(line), "false") {
							submitToRemoteRepo = false
							fmt.Print("\nPer your settings, you will not be sumbitting your work to a remote repo.")
						} else if strings.Contains(strings.ToLower(line), "true") {
							submitToRemoteRepo = true
						} else {
							fmt.Print(redText("\nYou entered something other than true or false for your submitToRemoteRepo setting. Please correct this."))
							os.Exit(1)
						}
					} else {
						fmt.Print(redText("\nUnrecognized setting detected. The line in question has this content: ", line))
						os.Exit(1)
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
					os.Exit(1)
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
				os.Exit(1)
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
		organizationSelected = strings.ReplaceAll(organizationSelected, " ", "-")
		organizationSelected = removeBannedSymbols.ReplaceAllString(organizationSelected, "")

		if organizationSelected == "" {
			fmt.Print(redText("Invalid input. You must input something here."))
			continue
		} else {
			break
		}
	}

	// Now that we know what the organization's name is, define its path.
	organizationPath = filepath.Join(gitRepoPath, "Customer-Engagements", organizationSelected)

	if submitToRemoteRepo {

		// And we can check if the remote repo exists! Fetch it now!
		exists, err := CheckIfGitLabProjectExistsAndFetch(organizationSelected, accessToken, organizationPath)
		if err != nil {
			fmt.Print(redText("\nError checking project existence: ", err))
			os.Exit(1)
		}

		if exists {
			// I've probably printed enough messages about the repo existing at this point, so I won't anymore.
			needToCreateRemoteGitRepo = false
		} else {
			fmt.Print("\nThe project does not exist.")
			needToCreateRemoteGitRepo = true
		}

		if needToCreateRemoteGitRepo {
			for {
				fmt.Print("\nEnter the organization's abrreviation. If it's unknown, leave it empty.\n")
				organizationAbbreviation, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Print(redText("\nExiting from user input."))
					} else {
						fmt.Print(redText("\nError reading line: ", err))
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
		}
	}

	if gitRepoPath != "" {

		// List existing contacts and setup auto-completion.
		var contactFolders []string

		if _, err := os.Stat(organizationPath); !os.IsNotExist(err) {
			files, err := os.ReadDir(organizationPath)
			if err != nil {
				fmt.Print(redText("\nError reading directory: ", err))
				os.Exit(1)
			} else {
				for _, f := range files {

					// We don't want your .git folders listed, thanks.
					if strings.HasPrefix(f.Name(), ".") {
						continue
					}

					if f.IsDir() {
						contactFolders = append(contactFolders, f.Name())
					}
				}

				// Only display the existing contacts message if there are valid folders found.
				if len(contactFolders) > 0 {
					fmt.Print("\n\nExisting contacts found:\n\n")
					for _, folderName := range contactFolders {
						fmt.Println("-", folderName)
					}
				}
			}
		}

		// Setup auto-completer with the valid folders found.
		completer := &FolderCompleter{Folders: contactFolders}
		rl.Config.AutoComplete = completer

		for {
			fmt.Print("\nEnter the organization's contact name. If it's unknown, leave it empty and it will populate as \"first-last\".\n")
			organizationContact, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
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
				organizationContact = strings.ReplaceAll(organizationContact, " ", "-")
				organizationContact = removeBannedSymbols.ReplaceAllString(organizationContact, "")
				break
			}
		}
	}

	if team == "install" {
		for {
			fmt.Print("Enter the Salesforce Case Number associated with these scripts. Press Enter to skip.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			// Don't accept anything other than numbers and blank input.
			if input == "" {
				break
			} else if _, err := strconv.Atoi(input); err == nil && input != "" {
				caseNumber, _ = strconv.Atoi(input)
				if caseNumber < 01000000 {
					fmt.Print(redText("\nAre you sure that's the right Case Number? It seems a bit too small.\n"))
					continue
				} else if caseNumber > 20000000 {
					fmt.Print(redText("\nAre you sure that's the right Case Number? It seems a bit too large.\n"))
					continue
				}
				break
			} else {
				fmt.Print(redText("\nInvalid input. You must input nothing or a valid case number."))
				continue
			}
		}
	}

	for {
		fmt.Print("Enter the number of clusters you'd like to make scripts for. Entering nothing will select 1.\n")
		input, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Print(redText("\nExiting from user input."))
			} else {
				fmt.Print(redText("\nError reading line: ", err))
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
				fmt.Print(redText("\nInvalid input. You've selected zero or less clusters to create scripts for.\n"))
				continue
			}
			break
		} else {
			fmt.Print(redText("\nInvalid input. Please enter an integer greater than zero.\n"))
			continue
		}
	}

	// Loop cluster creation for as many times as you specified.
	for i := 1; i <= clusterCount; i++ {
		for {
			fmt.Print("\nEnter cluster #", i, "'s name. Entering nothing will use \"HPC\"\n")
			clusterName, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}

			// The "profileName" is used for the cluster profile's name.
			profileName = strings.TrimSpace(clusterName)
			clusterName = strings.TrimSpace(strings.ToLower(clusterName))
			clusterName = strings.ReplaceAll(clusterName, " ", "-")
			clusterName = removeBannedSymbols.ReplaceAllString(clusterName, "")

			if clusterName == "" {
				clusterName = "HPC"
				break
			} else if lettersAndNumbersPattern.MatchString(clusterName) && clusterName != "" {
				fmt.Print(redText("\nInvalid input. You must include at least 1 letter or number in the cluster's name.\n"))
				continue
			} else {
				break
			}
		}

		// Map numbers to actual scheduler names.
		schedulerMap := map[int]string{
			1: "slurm",
			2: "pbs",
			3: "lsf",
			4: "gridengine",
			5: "htcondor",
			6: "awsbatch",
			7: "kubernetes",
		}

		for {
			fmt.Print("Select the scheduler you'd like to use by entering its corresponding number. Entering nothing will select Slurm.\n")
			fmt.Print("[1 Slurm] [2 PBS] [3 LSF] [4 Grid Engine] [5 HTCondor] [6 AWS] [7 Kubernetes]\n")
			schedulerSelected, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}

			if schedulerSelected == "" {
				schedulerSelected = "slurm"
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
				continue
			} else {
				schedulerSelected = schedulerMap[schedulerNumberSelected]
				break
			}
		}

		for {
			fmt.Print("Would you like to use include the custom MPI file? (y/n) Entering nothing will not include it.\n")
			customMPIInput, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}

			customMPIInput = strings.TrimSpace(strings.ToLower(customMPIInput))

			if customMPIInput == "y" || customMPIInput == "yes" {
				customMPI = true
				break
			} else if customMPIInput == "n" || customMPIInput == "no" || customMPIInput == "" {
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
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
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
				fmt.Print(redText("\nInvalid input. Enter a number between 1-3 to select a submission type.\n"))
				continue
			}
		}

		for {
			fmt.Print("Would you like to include the remote submission configuration files? (y/n) Entering nothing will exclude them.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(strings.ToLower(input))

			if input == "y" || input == "yes" {
				includeRemoteConfigFiles = true
				break
			} else if input == "n" || input == "no" || input == "" {
				break
			} else {
				fmt.Print(redText("Invalid input. You must enter one of the following: \"y\" or \"n\".\n"))
				continue
			}
		}

		for {
			fmt.Print("Enter the number of workers available on the cluster's license. Entering nothing will select 100,000.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Print(redText("\nExiting from user input."))
				} else {
					fmt.Print(redText("\nError reading line: ", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			if input == "" {
				numberOfWorkers = 100000
				break
			}

			// Don't accept anything other than numbers.
			if _, err := strconv.Atoi(input); err == nil {
				numberOfWorkers, _ = strconv.Atoi(input)
				if numberOfWorkers < 1 {
					fmt.Print(redText("\nInvalid input. You've selected zero or less workers.\n"))
					continue
				} else if numberOfWorkers > 100000 {
					fmt.Print(redText("\nInvalid input. You've selected more than 100000 workers, which is not offered on any license.\n"))
					continue
				} else if numberOfWorkers < 16 { // You've likely got bigger problems on your hands...
					fmt.Print(redText("\nMATLAB Parallel Server licenses typically aren't issued with and may not work with less than 16 seats. You likely have a bigger issue at hand...\n"))
					continue
				}

				break
			} else {
				fmt.Print(redText("Invalid input. You've likely included a character other than a number.\n"))
				continue
			}
		}

		if submissionType == "desktop" || submissionType == "both" {
			for {
				fmt.Print("What is the full filepath of MATLAB on the cluster? (ex: /usr/local/MATLAB/R2024a)\n")
				clusterMatlabRoot, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Print(redText("\nExiting from user input."))
					} else {
						fmt.Print(redText("\nError reading line: ", err))
						continue
					}
					return
				}
				clusterMatlabRoot = strings.TrimSpace(clusterMatlabRoot)

				if strings.Contains(clusterMatlabRoot, "/") || strings.Contains(clusterMatlabRoot, "\\") {
					break
				} else {
					fmt.Print(redText("Invalid filepath.\n"))
					continue
				}
			}

			for {
				fmt.Print("What is the hostname, FQDN, or IP address used to SSH to the cluster?\n")
				clusterHostname, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Print(redText("\nExiting from user input."))
					} else {
						fmt.Print(redText("\nError reading line: ", err))
						continue
					}
					return
				}
				clusterHostname = strings.TrimSpace(clusterHostname)

				if clusterHostname == "" {
					fmt.Print(redText("Invalid input. You must input something here."))
					continue
				} else {
					break
				}
			}
		}
		fmt.Print("\nCreating integration scripts for cluster #", i, "...")

		// This is where Big Things Part 1(tm) will happen.
		// These will be used in and out of if statements, so let's setup them up now.
		organizationContactPath = filepath.Join(organizationPath, organizationContact)
		tmpOrganizationContactPath = filepath.Join(tmpFolder, organizationContact)
		docPath := filepath.Join(tmpOrganizationContactPath, "doc")
		matlabPath := filepath.Join(tmpOrganizationContactPath, "scripts", schedulerSelected, releaseNumber, "matlab")
		IntegrationScriptsPath := filepath.Join(matlabPath, "IntegrationScripts")

		// Let's assume you aren't massively screwing with things. We should only need to do these things once.
		if i == 1 {

			// Copy new engagement files.
			tasks := []fileCopyTask{
				{sourceFile: filepath.Join("Utilities", "doc", "Getting_Started_With_Serial_And_Parallel_MATLAB.docx"), destinationFileName: "Getting_Started_With_Serial_And_Parallel_MATLAB.docx", destinationBasePath: docPath},
				{sourceFile: filepath.Join("Utilities", "doc", "README.txt"), destinationFileName: "README.txt", destinationBasePath: docPath},
				{sourceFile: filepath.Join("Utilities", "pub"), destinationFileName: "", destinationBasePath: filepath.Join(tmpOrganizationContactPath, "pub"), isDirectory: true},
			}

			for _, task := range tasks {
				sourceFilePath := filepath.Join(gitRepoPath, task.sourceFile)
				destFilePath := filepath.Join(task.destinationBasePath, task.destinationFileName)

				if task.isDirectory {
					err := copyDirectory(sourceFilePath, destFilePath)
					if err != nil {
						fmt.Print(redText("\nFailed to copy the directory: ", err))
						cleanUpTempFiles(tmpOrganizationContactPath)
					}
				} else {
					err := copyFile(sourceFilePath, destFilePath)
					if err != nil {
						fmt.Print(redText("\nFailed to copy the file: ", err))
						cleanUpTempFiles(tmpOrganizationContactPath)
					}
				}
			}
		}

		// Back to make cluster i's stuff!
		tasks := []fileCopyTask{
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "config-scripts", schedulerSelected, "bin"), destinationFileName: "", destinationBasePath: filepath.Join(tmpOrganizationContactPath, "scripts", schedulerSelected, releaseNumber, "bin"), isDirectory: true},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "+pctDebug", "ClientJavaLogging.p"), destinationFileName: "ClientJavaLogging.p", destinationBasePath: filepath.Join(matlabPath, "+pctDebug")},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "+pctDebug", "ClientJavaMessageHandler.p"), destinationFileName: "ClientJavaMessageHandler.p", destinationBasePath: filepath.Join(matlabPath, "+pctDebug")},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "+pctDebug", "Finalize.p"), destinationFileName: "Finalize.p", destinationBasePath: filepath.Join(matlabPath, "+pctDebug")},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "+pctDebug", "Init.p"), destinationFileName: "Init.p", destinationBasePath: filepath.Join(matlabPath, "+pctDebug")},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "helper-fcn", schedulerSelected), destinationFileName: "", destinationBasePath: matlabPath, isDirectory: true},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "helper-fcn", "common"), destinationFileName: "", destinationBasePath: matlabPath, isDirectory: true},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "conf-files"), destinationFileName: "", destinationBasePath: matlabPath, isDirectory: true},
			{sourceFile: filepath.Join(gitRepoPath, "Utilities", "matlab-files"), destinationFileName: "", destinationBasePath: matlabPath, isDirectory: true},
			{sourceFile: filepath.Join(scriptsPath, "matlab-parallel-"+schedulerSelected+"-plugin-main"), destinationFileName: "", destinationBasePath: filepath.Join(IntegrationScriptsPath, clusterName), isDirectory: true},
		}

		for i, task := range tasks {

			// They don't have anything special for these schedulers.
			if schedulerSelected == "awsbatch" || schedulerSelected == "kubernetes" || schedulerSelected == "htcondor" && (i == 0 || i == 5) {
				continue
			}

			destFilePath := filepath.Join(task.destinationBasePath, task.destinationFileName)

			if task.isDirectory {
				err := copyDirectory(task.sourceFile, destFilePath)
				if err != nil {
					fmt.Print(redText("\nFailed to copy the directory: ", err))
					cleanUpTempFiles(tmpOrganizationContactPath)
				}
			} else {
				err := copyFile(task.sourceFile, destFilePath)
				if err != nil {
					fmt.Print(redText("\nFailed to copy the file: ", err))
					cleanUpTempFiles(tmpOrganizationContactPath)
				}
			}
		}

		// Yes, the method I'm using is to delete the files after all possibly needed ones are copied.
		filesToDelete := []string{
			filepath.Join(matlabPath, "mdcs.rc"),
			filepath.Join(matlabPath, "licenseCheck.m"),
			filepath.Join(matlabPath, "parseGenericTemplateFile.m"),
			filepath.Join(IntegrationScriptsPath, clusterName, "discover"),
		}

		for _, fileToDelete := range filesToDelete {
			err := deleteFileOrFolder(fileToDelete)
			if err != nil {
				fmt.Println(redText("\nFailed to delete the file or folder: ", err))
				cleanUpTempFiles(tmpOrganizationContactPath)
			}
		}

		if !customMPI {
			fileToDelete := filepath.Join(matlabPath, "mpiLibConf.m")
			err := deleteFileOrFolder(fileToDelete)
			if err != nil {
				fmt.Print(redText("\nFailed to delete the file: ", err))
				cleanUpTempFiles(tmpOrganizationContactPath)
			}
		}

		if !includeRemoteConfigFiles {
			filesToDelete := []string{
				filepath.Join(matlabPath, "hpcRemoteCluster.conf"),
				filepath.Join(matlabPath, "hpcRemoteDesktop.conf"),
			}

			for _, fileToDelete := range filesToDelete {
				err := deleteFileOrFolder(fileToDelete)
				if err != nil {
					fmt.Println(redText("\nFailed to delete the file: ", err))
					cleanUpTempFiles(tmpOrganizationContactPath)
				}
			}
		}

		if submissionType == "cluster" {
			err := deleteFileOrFolder(filepath.Join(matlabPath, "hpcDesktop.conf"))
			if err != nil {
				fmt.Println(redText("\nFailed to delete the file: ", err))
				cleanUpTempFiles(tmpOrganizationContactPath)
			}
		} else if submissionType == "desktop" {
			err := deleteFileOrFolder(filepath.Join(matlabPath, "hpcCluster.conf"))
			if err != nil {
				fmt.Println(redText("\nFailed to delete the file: ", err))
				cleanUpTempFiles(tmpOrganizationContactPath)
			}
		}

		filesToModify := []string{
			"hpcDesktop.conf",
			"hpcCluster.conf",
			"hpcRemoteDesktop.conf",
			"hpcRemoteCluster.conf",
		}

		var stringNumberOfWorkers string = strconv.Itoa(numberOfWorkers) // Yes, I ended up just making it a string. Get over it.

		originalContent := map[string]string{
			"NumWorkers = 100000":  "NumWorkers = " + stringNumberOfWorkers,
			"ClusterMatlabRoot = ": "ClusterMatlabRoot = " + clusterMatlabRoot,
			"ClusterHost =":        "ClusterHost = " + clusterHostname,
			"cluster_name":         clusterName,
			"profile_name":         profileName,
			"QueueName = ":         "",
			"Partition = ":         "",
		}

		for i, fileToModify := range filesToModify {
			fileToModifyFullPath := filepath.Join(matlabPath, fileToModify)

			if !includeRemoteConfigFiles && (i == 2 || i == 3) {
				continue
			}

			if (submissionType == "desktop" && fileToModify == "hpcCluster.conf") || (submissionType == "cluster" && fileToModify == "hpcDesktop.conf") {
				continue
			}

			for contentToModify, modifiedContent := range originalContent {

				if (fileToModify == "hpcCluster.conf" || fileToModify == "hpcRemoteCluster") && contentToModify == "ClusterMatlabRoot = " {
					continue
				} else if fileToModify == "hpcCluster.conf" && contentToModify == "ClusterHost =" {
					continue
				}

				if (schedulerSelected == "pbs" || schedulerSelected == "lsf" || schedulerSelected == "gridengine") && contentToModify == "QueueName = " {
					continue
				} else if schedulerSelected == "slurm" && contentToModify == "Partition = " {
					continue
				}

				err = ModifyFileContents(fileToModifyFullPath, contentToModify, modifiedContent)
				if err != nil {
					fmt.Println(redText("\nFailed to modify the file: ", err))
					cleanUpTempFiles(tmpOrganizationContactPath)
				}
			}

			modifiedFileName := strings.ReplaceAll(fileToModifyFullPath, "hpc", clusterName)

			err = renameFile(fileToModifyFullPath, modifiedFileName)
			if err != nil {
				fmt.Println(redText("\nFailed to rename the file: ", err))
				cleanUpTempFiles(tmpOrganizationContactPath)
			}
		}

		fmt.Print("\nFinished script creation for cluster #", i, "!")
	}

	// Move everything to its permanent location.
	err = moveDirectory(tmpOrganizationContactPath, organizationContactPath)
	if err != nil {
		fmt.Println(redText("\nFailed to move the file: "), err)
		cleanUpTempFiles(tmpOrganizationContactPath)
	}

	// The needless README.md file.
	testFilePath := filepath.Join(organizationContactPath, "README.md")

	file, err := os.Create(testFilePath)
	if err != nil {
		fmt.Print(redText("\nError creating file: ", err))
		cleanUpTempFiles(tmpOrganizationContactPath)
	}
	defer file.Close()

	// Create the local repo, if needed.
	organizationDotGitFolder := filepath.Join(organizationPath, ".git")

	if _, err := os.Stat(organizationDotGitFolder); os.IsNotExist(err) {
		if err := createLocalGitRepo(organizationPath); err != nil {
			fmt.Println(redText("\nError creating local Git repo: ", err))
			os.Exit(1)
		}
	} else if err != nil {
		fmt.Print(redText("\nError checking if .git directory exists: ", err))
		os.Exit(1)
		return
	} else {
		fmt.Println("\n.git directory already exists.")
	}

	// This is where Big Things Part 2(tm) will happen (sort of.)
	if submitToRemoteRepo {
		fmt.Print("\nSubmitting to your remote Git repo...")

		// Create the repo on GitLab, if needed.
		if needToCreateRemoteGitRepo {
			projectURL, err := createGitLabRepo(organizationSelected, accessToken, gitRepoAPIURL, gitGroupID)
			if err != nil {
				fmt.Print(redText("\nError creating GitLab project: ", err))
				os.Exit(1)
				return
			}
			fmt.Print("\nGitLab project created: ", projectURL)
		} else { // Commit the changes made and push them to the remote repo.
			if err := remoteCommitAndPush(organizationPath, organizationSelected, gitUsername, accessToken); err != nil {
				fmt.Print(redText("\nError committing or pushing: ", err))
				os.Exit(1)
				return
			}
		}

		if needToCreateRemoteGitRepo {
			if err := publishMainBranch(organizationPath, organizationSelected, gitUsername, accessToken); err != nil {
				fmt.Print(redText("\nError publishing main branch: ", err))
				os.Exit(1)
				return
			}
		}
		fmt.Print("\nPushed to GitLab successfully.")
	}
	fmt.Print("\nFinished!")
}

func ModifyFileContents(filePath, oldText, newText string) error {

	// Open the file for reading.
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Use a StringBuilder to build the new file contents.
	var sb strings.Builder

	// Create a new scanner to read the file line by line.
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// "Ignore" commented-out lines.
		if strings.HasPrefix(line, "#") {
			sb.WriteString(line + "\n")
		} else {
			modifiedLine := strings.ReplaceAll(line, oldText, newText)

			// Only append non-empty lines that weren't already there. Delete the line otherwise.
			if modifiedLine != "" || line == "" {
				sb.WriteString(modifiedLine + "\n")
			}
		}
	}

	// Check for errors during scanning.
	if err := scanner.Err(); err != nil {
		return err
	}

	// Open the same file for writing; truncating it first.
	file, err = os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the modified contents back to the file.
	_, err = file.WriteString(sb.String())
	return err
}

func cleanUpTempFiles(tmpOrganizationContactPath string) error {
	redText := color.New(color.FgRed).SprintFunc()

	err := deleteFileOrFolder(tmpOrganizationContactPath)
	if err != nil {
		fmt.Print(redText("\nError deleting temporary engagement files: ", err))
		os.Exit(2)
	}
	os.Exit(2)
	return err
}

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

func renameFile(oldPath, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if err != nil {
		return err
	}
	return nil
}

func moveDirectory(src, dst string) error {

	// First, copy the directory and its contents to the new location.
	err := copyDirectory(src, dst)
	if err != nil {
		return err
	}

	// Then, remove the original directory.
	err = os.RemoveAll(src)
	if err != nil {
		return err
	}

	return nil
}

func deleteFileOrFolder(file string) error {
	err := os.RemoveAll(file)
	if err != nil {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {

	// Ensure the destination directory exists.
	destDir := filepath.Dir(dst)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Open the source file for reading.
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create the destination file.
	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	// Copy the contents of the source file to the destination file.
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Ensure that any writes to the destination file are synced.
	err = destFile.Sync()
	return err
}

func copyDirectory(srcDir, destDir string) error {
	// Create the destination directory, if we haven't already.
	err := os.MkdirAll(destDir, 0755)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		destPath := filepath.Join(destDir, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectories.
			err = copyDirectory(srcPath, destPath)
			if err != nil {
				return err
			}
		} else {
			// Copy files.
			err = copyFile(srcPath, destPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func CheckIfGitLabProjectExistsAndFetch(organizationSelected, accessToken, localRepoPath string) (bool, error) {

	redText := color.New(color.FgRed).SprintFunc()
	urlToCheck := gitRepoAPIURL + gitGroupName + "%2F" + organizationSelected

	// Need to remove everything after .com/ in your API URL for the cloneURL.
	parts := strings.Split(gitRepoAPIURL, ".com")
	baseURL := ""
	if len(parts) > 0 {
		baseURL = parts[0] + ".com"
	} else {
		return false, fmt.Errorf("'.com' not found in your gitRepoAPIURL")
	}

	cloneURL := fmt.Sprintf("%s/%s/%s.git", baseURL, gitGroupName, organizationSelected)

	fmt.Println("\nChecking this project to see if it exists: " + urlToCheck)

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
		fmt.Println("Project exists.")

		// Check if localRepoPath exists
		if _, err := os.Stat(localRepoPath); os.IsNotExist(err) {
			fmt.Println("Local repository path does not exist. Cloning repository...")

			// Clone the repository.
			_, err := git.PlainClone(localRepoPath, false, &git.CloneOptions{
				URL:      cloneURL,
				Progress: os.Stdout, // Show progress
				Auth: &githttp.BasicAuth{
					Username: gitUsername,
					Password: accessToken,
				},
			})
			if err != nil {
				return true, fmt.Errorf("failed to clone repository: %w", err)
			}
			fmt.Println("Repository cloned.")
		} else {

			// If the directory exists, attempt to open the repository.
			r, err := git.PlainOpen(localRepoPath)
			if err != nil {
				return true, fmt.Errorf("failed to open local repository: %w", err)
			}

			// Fetch updates from the remote repository.
			err = fetchUpdates(r)
			if err != nil {
				fmt.Print(redText("\nFailed to fetch updates: ", err))
				os.Exit(1)
			}
		}

		return true, nil
	}

	// Assume other status codes are errors and treat them as so.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	return false, fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(body))
}

func fetchUpdates(r *git.Repository) error {

	// Sometimes shit is hard.
	redText := color.New(color.FgRed).SprintFunc()

	fmt.Print("\nFetching updates...")

	// Get the remote configuration
	remote, err := r.Remote("origin")
	if err != nil {
		fmt.Print(redText("\nFailed to get remote origin: ", err))
		os.Exit(1)
	}

	// Fetch the latest changes from the remote repository with authentication
	err = remote.Fetch(&git.FetchOptions{
		Auth: &githttp.BasicAuth{
			Username: gitUsername,
			Password: accessToken,
		},
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Force:    true,
	})

	if err != nil && err != git.NoErrAlreadyUpToDate {
		fmt.Print(redText("\nFailed to fetch updates: ", err))
	}

	fmt.Print("\nFetch completed.")
	return nil
}

func createLocalGitRepo(folderPath string) error {

	r, err := git.PlainInit(folderPath, false)
	if err != nil {
		return err
	}

	// Work with the repository's worktree.
	w, err := r.Worktree()
	if err != nil {
		return err
	}

	// Add all files in the folder to the staging area.
	err = w.AddWithOptions(&git.AddOptions{All: true})
	if err != nil {
		return err
	}

	// Check if there are any changes staged.
	status, err := w.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		fmt.Println("No changes to commit.")
		return nil
	}

	// Make an initial commit to the "main" branch
	_, err = w.Commit("Initial commit.", &git.CommitOptions{
		Author: &object.Signature{
			Name:  gitUsername,
			Email: gitEmailAddress,
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	// Create a new "main" branch reference pointing to the commit just created
	headRef, err := r.Head()
	if err != nil {
		return err
	}
	mainRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), headRef.Hash())
	err = r.Storer.SetReference(mainRef)
	if err != nil {
		return err
	}

	// Update HEAD to point to the "main" branch
	err = r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	if err != nil {
		return err
	}

	return nil
}

func createGitLabRepo(projectName, accessToken, gitRepoAPIURL string, namespaceID int) (string, error) {
	gitRepoAPIWithNoProjectURL := gitRepoAPIURL[:len(gitRepoAPIURL)-9]
	git, err := gitlab.NewClient(accessToken, gitlab.WithBaseURL(gitRepoAPIWithNoProjectURL))
	if err != nil {
		return "", err
	}

	// Create the project.
	project, _, err := git.Projects.CreateProject(&gitlab.CreateProjectOptions{
		Name:        &projectName,
		NamespaceID: gitlab.Ptr(namespaceID),
	})
	if err != nil {
		return "", err
	}

	// Create its abbreviation variable.
	_, _, err = git.ProjectVariables.CreateVariable(project.ID, &gitlab.CreateProjectVariableOptions{
		Key:   gitlab.Ptr("abbreviation"),
		Value: gitlab.Ptr(organizationAbbreviation),
	})
	if err != nil {
		return "", err
	}

	return project.WebURL, nil
}

func remoteCommitAndPush(folderPath, projectName, gitUsername, accessToken string) error {

	// Need to remove everything after .com in your API URL for the constructedURL.
	parts := strings.Split(gitRepoAPIURL, ".com")
	baseURL := ""
	if len(parts) > 0 {
		baseURL = parts[0] + ".com"
	} else {
		return fmt.Errorf("'.com' not found in your gitRepoAPIURL")
	}

	constructedURL := fmt.Sprintf("%s/%s/%s.git", baseURL, gitGroupName, projectName)

	fmt.Printf("\nProject URL to commit to: %s", constructedURL)

	r, err := git.PlainOpen(folderPath)
	if err != nil {
		return err
	}

	// Check if the remote branch "origin" already exists.
	_, err = r.Remote("origin")
	if err != nil {
		// Create it if it doesn't exist.
		_, err = r.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{constructedURL},
		})
		if err != nil {
			return err
		}
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

	// Check if there are any changes staged.
	status, err := w.Status()
	if err != nil {
		return err
	}
	if status.IsClean() {
		fmt.Println("\nNo changes to commit remotely.")
		return nil
	}

	// Commit the changes.
	_, err = w.Commit(gitExistingRepoCommitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  gitUsername,
			Email: gitEmailAddress,
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	// Push the changes to the remote.
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
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

func publishMainBranch(folderPath, projectName, gitUsername, accessToken string) error {

	// Need to remove everything after .com/ in your API URL for the constructedURL.
	parts := strings.Split(gitRepoAPIURL, ".com")
	baseURL := ""
	if len(parts) > 0 {
		baseURL = parts[0] + ".com"
	} else {
		return fmt.Errorf("'.com' not found in your gitRepoAPIURL")
	}

	constructedURL := fmt.Sprintf("%s/%s/%s.git", baseURL, gitGroupName, projectName)
	fmt.Printf("\nPreparing to publish 'main' branch to: %s\n", constructedURL)

	// Open the existing repo.
	r, err := git.PlainOpen(folderPath)
	if err != nil {
		return fmt.Errorf("failed to open local repository: %v", err)
	}

	// Ensure the remote is correctly set up.
	_, err = r.Remote("origin")
	if err != nil {
		// Remote branch "origin" somehow does not already exist, so let's create it!
		_, err = r.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{constructedURL},
		})
		if err != nil {
			return fmt.Errorf("failed to create remote 'origin': %v", err)
		}
	}

	// Push 'main' branch to remote 'origin', setting it as upstream.
	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
		Auth: &githttp.BasicAuth{
			Username: gitUsername,
			Password: accessToken,
		},
	})
	if err != nil {
		if err == git.NoErrAlreadyUpToDate {
			fmt.Println("The 'main' branch is already up to date with the remote.")
		} else {
			return fmt.Errorf("failed to push 'main' branch to remote: %v", err)
		}
	} else {
		fmt.Println("Successfully published 'main' branch to remote repository.")
	}

	return nil
}
