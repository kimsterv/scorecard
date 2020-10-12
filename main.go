package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dlorenc/scorecard/checker"
	"github.com/dlorenc/scorecard/checks"
	"github.com/dlorenc/scorecard/roundtripper"
	"github.com/google/go-github/v32/github"
)

var repo = flag.String("repo", "", "url to the repo")
var csvFile = flag.String("file", "", "csv file of projects")
var checksToRun = flag.String("checks", "", "specific checks to run, instead of all")

type result struct {
	cr   checks.CheckResult
	name string
}

func main() {
	flag.Parse()

	// Open the file
	recordFile, err := os.Open(*csvFile)
	if err != nil {
		log.Println("An error encountered ::", err)
		return
	}

	// Setup the reader
	reader := csv.NewReader(recordFile)
	// Read header row
	reader.Read()

	// Open the output file
	outputFile, err := os.Create("scorecard_output.csv")
	if err != nil {
		fmt.Println("Error while creating the file ::", err)
		return
	}

	// Initialize the writer
	writer := csv.NewWriter(outputFile)

	//i := 1
	for /*i <= 5*/ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		//log.Printf("Row : %v \n", record)
		recordRepo := record[1]

		//log.Printf("splitR %s", recordRepo)
		recordRepo = strings.Replace(recordRepo, "https://", "", 2)
		//log.Printf("splitR %s", recordRepo)

		split := strings.SplitN(recordRepo, "/", 3)
		//log.Printf("split %s", split)

		host, owner, repo := split[0], split[1], split[2]
		log.Printf("repo %s", repo)

		switch host {
		case "github.com":
		default:
			log.Fatalf("unsupported host: %s", host)
		}

		ctx := context.Background()

		// Use our custom roundtripper
		rt := roundtripper.NewTransport(ctx)

		client := &http.Client{
			Transport: rt,
		}
		ghClient := github.NewClient(client)

		c := &checker.Checker{
			Ctx:        ctx,
			Client:     ghClient,
			HttpClient: client,
			Owner:      owner,
			Repo:       repo,
		}

		resultsCh := make(chan result)
		wg := sync.WaitGroup{}
		for _, check := range checks.AllChecks {
			check := check
			wg.Add(1)
			log.Printf("Starting [%s]\n", check.Name)
			go func() {
				defer wg.Done()
				var r checks.CheckResult
				for retriesRemaining := 3; retriesRemaining > 0; retriesRemaining-- {
					r = check.Fn(c)
					if r.ShouldRetry {
						log.Println(r.Error)
						continue
					}
					break
				}
				resultsCh <- result{
					name: check.Name,
					cr:   r,
				}
			}()
		}
		go func() {
			wg.Wait()
			close(resultsCh)
		}()

		// Collect results
		results := []result{}

		for result := range resultsCh {
			log.Printf("Finished [%s]\n", result.name)
			results = append(results, result)
		}

		// Sort them by name
		sort.Slice(results, func(i, j int) bool {
			return results[i].name < results[j].name
		})

		csvResults := []string{}

		// Append original CSV data
		for x := 0; x < 2; x++ {
			csvResults = append(csvResults, record[x])
			fmt.Printf("appending: %s\n", record[x])
		}

		fmt.Println()
		fmt.Println("RESULTS")
		fmt.Println("-------")
		for _, r := range results {
			fmt.Println(r.name, r.cr.Pass, r.cr.Confidence)
			csvResults = append(csvResults, strconv.FormatBool(r.cr.Pass))
			//fmt.Printf("appending: %s\n", strconv.FormatBool(r.cr.Pass))
			csvResults = append(csvResults, strconv.Itoa(r.cr.Confidence))
			//fmt.Printf("appending: %s\n", strconv.Itoa(r.cr.Confidence))
		}

		// Write out to csv
		if err := writer.Write(csvResults); err != nil {
			log.Fatalln("error writing record to csv:", err)
		}

		//i++

	}

	// Write any buffered data to the underlying writer (standard output).
	writer.Flush()

	if err := writer.Error(); err != nil {
		log.Fatal(err)
	}

	err = outputFile.Close()
	if err != nil {
		fmt.Println("Error while closing the file ::", err)
		return
	}
}
