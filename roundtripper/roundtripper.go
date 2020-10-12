package roundtripper

import (
	"bytes"
	"context"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const GITHUB_AUTH_TOKEN = "GITHUB_AUTH_TOKEN"

var mutex = &sync.Mutex{}

// RateLimitRoundTripper is a rate-limit aware http.Transport for Github.
type RateLimitRoundTripper struct {
	InnerTransport http.RoundTripper
}

// NewTransport returns a configured http.Transport for use with GitHub
func NewTransport(ctx context.Context) http.RoundTripper {
	token := os.Getenv(GITHUB_AUTH_TOKEN)

	// Start with oauth
	transport := http.DefaultTransport
	if token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		transport = oauth2.NewClient(ctx, ts).Transport
	}

	// Wrap that with the rate limiter
	rateLimit := &RateLimitRoundTripper{
		InnerTransport: transport,
	}

	// Wrap that with the response cacher
	cache := &CachingRoundTripper{
		innerTransport: rateLimit,
		respCache:      map[url.URL]*http.Response{},
		bodyCache:      map[url.URL][]byte{},
	}

	return cache
}

// Roundtrip handles caching and ratelimiting of responses from GitHub.
func (gh *RateLimitRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := gh.InnerTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	rateLimit := resp.Header.Get("X-RateLimit-Remaining")
	remaining, err := strconv.Atoi(rateLimit)
	if err != nil {
		return resp, nil
	}

	if remaining <= 0 {
		reset, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Reset"))
		if err != nil {
			return resp, nil
		}

		duration := time.Until(time.Unix(int64(reset), 0))
		log.Printf("Rate limit exceeded. Waiting %s to retry...", duration)

		// Retry
		time.Sleep(duration)
		log.Print("Rate limit exceeded. Retrying...")
		return gh.RoundTrip(r)
	}

	return resp, err
}

type CachingRoundTripper struct {
	innerTransport http.RoundTripper
	respCache      map[url.URL]*http.Response
	bodyCache      map[url.URL][]byte
}

func (rt *CachingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	// Check the cache
	mutex.Lock()
	resp, ok := rt.respCache[*r.URL]
	mutex.Unlock()
	if ok {
		log.Printf("Cache hit on %s", r.URL.String())
		mutex.Lock()
		resp.Body = ioutil.NopCloser(bytes.NewReader(rt.bodyCache[*r.URL]))
		mutex.Unlock()
		return resp, nil
	}

	// Get the real value
	mutex.Lock()
	resp, err := rt.innerTransport.RoundTrip(r)
	mutex.Unlock()
	if err != nil {
		return nil, err
	}

	// Add to cache
	if resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		mutex.Lock()
		rt.respCache[*r.URL] = resp
		mutex.Unlock()
		mutex.Lock()
		rt.bodyCache[*r.URL] = body
		mutex.Unlock()

		resp.Body = ioutil.NopCloser(bytes.NewReader(body))
	}
	return resp, err
}
