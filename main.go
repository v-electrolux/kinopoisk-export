package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	SetPageSizeUrlPattern        = "https://www.kinopoisk.ru/user/%s/votes/list/vs/novote/perpage/200/"
	UrlPattern                   = "https://www.kinopoisk.ru/user/%s/votes/list/vs/novote/page/%d/"
	MovieWatchedUrlPattern       = "https://graphql.kinopoisk.ru/graphql/?operationName=MovieSetWatched"
	MovieSetWatchedOperationName = "MovieSetWatched"
	MovieSetWatchedQuery         = "mutation MovieSetWatched($movieId: Long!) { movie { watched { set(input: {movieId: $movieId}) { error { message __typename } status __typename } __typename } __typename } } "
)

var movies map[string]string
var cookie string
var userId string

type WatchedBody struct {
	OperationName string    `json:"operationName"`
	Variables     Variables `json:"variables"`
	Query         string    `json:"query"`
}

type Variables struct {
	MovieID int `json:"movieId"`
}

type WatchedResponse struct {
	Data WatchedResponseData `json:"data"`
}

type WatchedResponseData struct {
	Movie WatchedResponseMovie `json:"movie"`
}

type WatchedResponseMovie struct {
	Watched WatchedResponseWatched `json:"watched"`
}

type WatchedResponseWatched struct {
	Set WatchedResponseSet `json:"set"`
}

type WatchedResponseSet struct {
	Error  string `json:"error"`
	Status string `json:"status"`
}

func main() {
	var outputFilename = flag.String("o", "", "path to output file with dumped movies")
	var cookieFlag = flag.String("c", "", "cookie header (you must copy it from browser)")
	var inputFilename = flag.String("i", "", "path to file with movies")
	var userIdFlag = flag.String("u", "", "kinopoisk user id")
	flag.Parse()
	cookie = *cookieFlag
	userId = *userIdFlag

	movies = map[string]string{}

	if outputFilename != nil && *outputFilename != "" {
		export(*outputFilename)
	} else if inputFilename != nil && *inputFilename != "" {
		importMovies(*inputFilename)
	} else {
		flag.PrintDefaults()
	}
}

func importMovies(inputFilename string) {
	data := readMoviesCsv(inputFilename)

	for _, line := range data {
		movieId := line[0]
		movieName := line[1]

		movieIdInt, err := strconv.Atoi(movieId)
		if err != nil {
			fmt.Println("Convert movie id to int:", err)
			return
		}

		ok := setMovieWatched(movieIdInt)
		if !ok {
			fmt.Printf("Movie %s not set watched\n", movieName)
		} else {
			fmt.Printf("Movie %s set watched\n", movieName)
		}
		time.Sleep(1 * time.Second)
	}
}

func setMovieWatched(movieId int) bool {
	url := MovieWatchedUrlPattern
	bodyStruct := WatchedBody{
		OperationName: MovieSetWatchedOperationName,
		Variables: Variables{
			MovieID: movieId,
		},
		Query: MovieSetWatchedQuery,
	}
	body, err := json.Marshal(bodyStruct)
	if err != nil {
		fmt.Println("Marshal body:", err)
		return false
	}

	resp, err := makeReq(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Println("Error fetching URL:", err)
		return false
	}
	defer resp.Body.Close()

	respByte, err := io.ReadAll(resp.Body)
	var respStruct WatchedResponse
	err = json.Unmarshal(respByte, &respStruct)
	if err != nil {
		fmt.Println("Error parsing json:", err)
		return false
	}

	if respStruct.Data.Movie.Watched.Set.Status != "SUCCESS" {
		return false
	}

	return true
}

func readMoviesCsv(inputFilename string) [][]string {
	file, err := os.Open(inputFilename)
	if err != nil {
		log.Fatal(err)
	}

	defer file.Close()

	csvReader := csv.NewReader(file)
	csvReader.Comma = ';'
	data, err := csvReader.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	return data
}

func export(outputFilename string) {
	url := fmt.Sprintf(SetPageSizeUrlPattern, userId)
	totalMovies, pageSize := parseFirstPage(url)
	pageCount := totalMovies / pageSize
	if totalMovies%pageSize != 0 {
		pageCount++
	}
	fmt.Println("Watched movies count: ", totalMovies)
	fmt.Println("Page size: ", pageSize)
	fmt.Println("Pages count: ", pageCount)

	var parsedMovies, totalParsedMovies int
	for i := 1; i < pageCount+1; i++ {
		if i == 1 {
			url = fmt.Sprintf(SetPageSizeUrlPattern, userId)
		} else {
			url = fmt.Sprintf(UrlPattern, userId, i)
		}
		parsedMovies = parsePage(url)
		totalParsedMovies = totalParsedMovies + parsedMovies
		time.Sleep(5 * time.Second)
	}

	fmt.Println("")
	fmt.Println("Watched movies parsed: ", totalParsedMovies)
	fmt.Println("Movies dumped count: ", len(movies))
	dumpFile(outputFilename, movies)
}

func parseFirstPage(url string) (int, int) {
	var totalMovies, pageSize int
	for i := 0; totalMovies == 0; i++ {
		if i == 0 {
			fmt.Printf("Fetch first page for meta: %s\n", url)
		} else {
			fmt.Printf("Try fetch first page (try #%d): %s\n", i+1, url)
		}

		resp, err := makeReq(http.MethodGet, url, nil)
		if err != nil {
			fmt.Println("Error fetching URL:", err)
			return 0, 0
		}
		defer resp.Body.Close()
		doc, err := html.Parse(resp.Body)
		if err != nil {
			fmt.Println("Error parsing HTML:", err)
			return 0, 0
		}
		totalMovies, pageSize = findPagingHeader(doc)
	}
	return totalMovies, pageSize
}

func findPagingHeader(n *html.Node) (int, int) {
	if n.Type == html.ElementNode && n.Data == "div" {
		if isClassName(n, "pagesFromTo") {
			pages := n.FirstChild.Data
			pagesParts := strings.Split(pages, " из ")
			pageRangeParts := strings.Split(pagesParts[0], "—")

			firstItemNumber, err := strconv.Atoi(pageRangeParts[0])
			if err != nil {
				fmt.Println("Error parsing HTML:", err)
				return 0, 0
			}

			lastItemNumber, err := strconv.Atoi(pageRangeParts[1])
			if err != nil {
				fmt.Println("Error parsing HTML:", err)
				return 0, 0
			}
			pageSize := lastItemNumber - firstItemNumber + 1

			totalMoviesStr := pagesParts[1]
			totalMovies, err := strconv.Atoi(totalMoviesStr)
			if err != nil {
				fmt.Println("Error parsing HTML:", err)
				return 0, 0
			}
			return totalMovies, pageSize
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		totalMovies, pageSize := findPagingHeader(c)
		if totalMovies != 0 {
			return totalMovies, pageSize
		}
	}
	return 0, 0
}

func parsePage(url string) int {
	var foundMovies int
	for i := 0; foundMovies == 0; i++ {
		fmt.Printf("\n")
		if i == 0 {
			fmt.Printf("Fetch page: %s ...", url)
		} else {
			fmt.Printf("Try fetch page (try #%d): %s", i+1, url)
		}
		resp, err := makeReq(http.MethodGet, url, nil)
		if err != nil {
			fmt.Println("Error fetching URL:", err)
			return 0
		}
		defer resp.Body.Close()
		doc, err := html.Parse(resp.Body)
		if err != nil {
			fmt.Println("Error parsing HTML:", err)
			return 0
		}
		foundMovies = findMovies(doc)
		if foundMovies != 0 {
			fmt.Printf("parsed %d movies, sleep for 5 sec...", foundMovies)
		} else {
			fmt.Printf("parsed %d movies, sleep for 5 sec and retry", foundMovies)
		}
		time.Sleep(5 * time.Second)
	}

	return foundMovies
}

func findMovies(n *html.Node) int {
	if n.Type == html.ElementNode && n.Data == "div" {
		if isClassName(n, "profileFilmsList") {
			parsedMovies := processMovies(n)
			return parsedMovies
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		parsedMovies := findMovies(c)
		if parsedMovies != 0 {
			return parsedMovies
		}
	}
	return 0
}

func processMovies(n *html.Node) int {
	var parsedMovies int
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isClassName(c, "item") || isClassName(c, "item even") {
			ok := processMovie(c)
			if ok {
				parsedMovies++
			}
		}
	}
	return parsedMovies
}

func isClassName(n *html.Node, className string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && strings.Contains(a.Val, className) {
			return true
		}
	}

	return false
}

func processMovie(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isClassName(c, "info") {
			for d := c.FirstChild; d != nil; d = d.NextSibling {
				if isClassName(d, "nameRus") {
					for e := d.FirstChild; e != nil; e = e.NextSibling {
						if e.Type == html.ElementNode && e.Data == "a" {
							for _, a := range e.Attr {
								if a.Key == "href" {
									movies[extractId(a.Val)] = e.FirstChild.Data
									return true
								}
							}
						}
					}
				}
			}
		}
	}
	return false
}

func extractId(href string) string {
	parts := strings.Split(href, "/")
	return parts[2]
}

func makeReq(method, url string, body io.Reader) (resp *http.Response, err error) {
	client := &http.Client{}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Add("Cookie", cookie)
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Origin", "https://www.kinopoisk.ru")
		req.Header.Add("Referer", "https://www.kinopoisk.ru/")
		req.Header.Add("Service-Id", "25")
		req.Header.Add("Source-Id", "1")
	}

	return client.Do(req)
}

func dumpFile(filename string, records map[string]string) {
	writer, file, err := createCSVWriter(filename)
	if err != nil {
		fmt.Println("Error creating CSV writer:", err)
		return
	}
	defer file.Close()
	for movieId, movieName := range records {
		writeCSVRecord(writer, []string{movieId, movieName})
	}
	// Flush the writer and check for any errors
	writer.Flush()
	if err := writer.Error(); err != nil {
		fmt.Println("Error flushing CSV writer:", err)
	}
}

func createCSVWriter(filename string) (*csv.Writer, *os.File, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, nil, err
	}

	writer := csv.NewWriter(f)
	writer.Comma = ';'
	return writer, f, nil
}

func writeCSVRecord(writer *csv.Writer, record []string) {
	err := writer.Write(record)
	if err != nil {
		fmt.Println("Error writing record to CSV:", err)
	}
}
