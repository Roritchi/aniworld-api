package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/agnivade/levenshtein"
	"github.com/gin-gonic/gin"
)

const BASE_URL = "https://aniworld.to"

var animesCached []AnimeEntry

type AnimeEntry struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	AlternativeTitles []string `json:"alternative_titles"`
	LinkPath          string   `json:"link_path"`
}

type AnimeInfo struct {
	Thumbnail string         `json:"thumbnail"`
	Title     string         `json:"title"`
	Summary   string         `json:"summary"`
	Episodes  []EpisodeEntry `json:"episodes"`
}

type EpisodeEntry struct {
	LinkPath       string `json:"link_path"`
	Title          string `json:"title"`
	SecondaryTitle string `json:"secondary_title"`
	EpisodeNr      string `json:"episode"`
	SeasonNr       string `json:"season"`
}

func parseSeason(doc *goquery.Document) []EpisodeEntry {
	var episodes []EpisodeEntry

	season := doc.Find("meta[itemprop='seasonNumber']").First().AttrOr("content", "")

	doc.Find("[itemprop='episode']").Each(func(index int, item *goquery.Selection) {
		anchor := item.Find(".seasonEpisodeTitle a").First()
		link, _ := anchor.Attr("href")
		titleSearch := anchor.Find("strong,span")
		bestTitle := titleSearch.First().Text()
		secondaryTitle := titleSearch.Last().Text()

		episode := EpisodeEntry{
			LinkPath:       link,
			Title:          bestTitle,
			SecondaryTitle: secondaryTitle,
			EpisodeNr:      item.AttrOr("data-episode-season-id", ""),
			SeasonNr:       season,
		}

		episodes = append(episodes, episode)
	})

	return episodes
}

func parseShow(animeId string) AnimeInfo {
	res, err := http.Get(BASE_URL + "/anime/stream/" + animeId)
	if err != nil {
		fmt.Printf("error making http request: %s\n", err)
		os.Exit(1)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	info := AnimeInfo{
		Thumbnail: BASE_URL + doc.Find(".seriesCoverBox img").First().AttrOr("data-src", ""),
		Title:     doc.Find(".series-title h1 span").First().Text(),
		Summary:   doc.Find("[itemprop='accessibilitySummary']").First().AttrOr("data-full-description", ""),
	}

	episodes := parseSeason(doc)

	doc.Find("#stream ul:first-child li a:not(.active)").Each(func(index int, item *goquery.Selection) {
		link, exists := item.Attr("href")
		if exists {
			res, err := http.Get(BASE_URL + link)
			if err != nil {
				fmt.Printf("error making http request: %s\n", err)
				os.Exit(1)
			}

			doc, err := goquery.NewDocumentFromReader(res.Body)
			if err != nil {
				log.Fatal(err)
			}

			episodes = append(episodes, parseSeason(doc)...)
		}
	})

	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].SeasonNr == episodes[j].SeasonNr {
			ii, _ := strconv.Atoi(episodes[i].EpisodeNr)
			ji, _ := strconv.Atoi(episodes[j].EpisodeNr)
			return ii < ji
		}
		ii, _ := strconv.Atoi(episodes[i].SeasonNr)
		ji, _ := strconv.Atoi(episodes[j].SeasonNr)
		return ii < ji
	})

	info.Episodes = episodes

	return info
}

func hasExactWordMatch(phrase string, titles []string) bool {
	phraseWords := strings.Fields(strings.ToLower(phrase))

	for _, title := range titles {
		titleWords := strings.Fields(strings.ToLower(title))
		for _, pw := range phraseWords {
			for _, tw := range titleWords {
				if pw == tw {
					return true
				}
			}
		}
	}
	return false
}

func setupRouter() *gin.Engine {
	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Ping test
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	r.GET("/animes", func(c *gin.Context) {
		res, err := http.Get(BASE_URL + "/animes")
		if err != nil {
			fmt.Printf("error making http request: %s\n", err)
			os.Exit(1)
		}

		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			log.Fatal(err)
		}

		var entries []AnimeEntry

		doc.Find("#seriesContainer li a").Each(func(index int, item *goquery.Selection) {
			link := item.AttrOr("href", "")
			id, _ := strings.CutPrefix(link, "/anime/stream/")

			var alternativeTitles []string
			for _, value := range strings.Split(item.AttrOr("data-alternative-title", ""), ",") {
				alternativeTitles = append(alternativeTitles, strings.TrimSpace(value))
			}

			entry := AnimeEntry{
				ID:                id,
				Title:             item.Text(),
				AlternativeTitles: alternativeTitles,
				LinkPath:          link,
			}
			entries = append(entries, entry)
		})

		animesCached = entries

		c.JSON(http.StatusOK, entries)
	})

	r.GET("/play", func(c *gin.Context) {
		linkPath := c.Query("link_path")

		res, err := http.Get(BASE_URL + linkPath)
		if err != nil {
			fmt.Printf("error making http request: %s\n", err)
			os.Exit(1)
		}

		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			log.Fatal(err)
		}

		watchUrl := doc.Find(".generateInlinePlayer a.watchEpisode").First().AttrOr("href", "")

		res, err = http.Get("http://localhost:3000/?url=" + BASE_URL + watchUrl)
		if err != nil {
			fmt.Printf("error making http request: %s\n", err)
			os.Exit(1)
		}
		defer res.Body.Close()

		// Optional: check status code
		if res.StatusCode != http.StatusOK {
			panic(fmt.Sprintf("unexpected status: %s", res.Status))
		}

		// Read and decode
		var data map[string]interface{}
		err = json.NewDecoder(res.Body).Decode(&data)
		if err != nil {
			panic(err)
		}

		c.JSON(http.StatusOK, data)
	})

	r.GET("/search", func(c *gin.Context) {
		var result []AnimeEntry

		search := c.Query("phrase")

		result = append(result, animesCached...)

		sort.Slice(result, func(i, j int) bool {
			a := result[i]
			b := result[j]

			titlesA := append([]string{a.Title}, a.AlternativeTitles...)
			titlesB := append([]string{b.Title}, b.AlternativeTitles...)

			exactA := hasExactWordMatch(search, titlesA)
			exactB := hasExactWordMatch(search, titlesB)

			if exactA && !exactB {
				return true
			} else if !exactA && exactB {
				return false
			}

			// fallback: Levenshtein
			distance := func(titles []string) int {
				best := math.MaxInt
				for _, title := range titles {
					if d := levenshtein.ComputeDistance(search, title); d < best {
						best = d
					}
				}
				return best
			}

			return distance(titlesA) < distance(titlesB)
		})

		max := 20
		if len(result) > max {
			result = result[:max]
		}

		c.JSON(http.StatusOK, result)
	})

	r.GET("/anime/:id", func(c *gin.Context) {
		animeId := c.Params.ByName("id")

		animeInfo := parseShow(animeId)

		if strings.TrimSpace(animeInfo.Thumbnail) != "" {
			out, err := os.Create("./cache/" + animeId)
			if err != nil {
				log.Println(err)
			}
			defer out.Close()

			// Get the data
			resp, err := http.Get(animeInfo.Thumbnail)
			if err != nil {
				log.Println(err)
			}
			defer resp.Body.Close()

			// Write the body to file
			_, err = io.Copy(out, resp.Body)
			if err != nil {
				log.Println(err)
			}
		}

		c.JSON(http.StatusOK, animeInfo)
	})

	r.GET("/thumbnail/:id", func(c *gin.Context) {
		filePath := c.Param("id")
		fullPath := path.Join("./cache", filePath)

		if _, err := os.Stat(fullPath); err == nil {
			// File exists → serve it
			c.File(fullPath)
			return
		}

		// Fallback logic if file not found
		animeId := c.Params.ByName("id")

		animeInfo := parseShow(animeId)

		out, err := os.Create("./cache/" + animeId)
		if err != nil {
			log.Println(err)
		}
		defer out.Close()

		// Get the data
		resp, err := http.Get(animeInfo.Thumbnail)
		if err != nil {
			log.Println(err)
		}
		defer resp.Body.Close()

		// Write the body to file
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			log.Println(err)
		}

		c.File("./cache/" + animeId)
	})

	return r
}

func main() {
	r := setupRouter()
	// Listen and Server in 0.0.0.0:8080
	r.Run("0.0.0.0:3333")
}
