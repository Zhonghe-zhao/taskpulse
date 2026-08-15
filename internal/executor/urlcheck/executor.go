package urlcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
)

const (
	maxURLs              = 100
	maxResponseDrainSize = 64 << 10
	defaultConcurrency   = 5
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type Executor struct {
	client         HTTPDoer
	maxConcurrency int
	now            func() time.Time
}

func New(client HTTPDoer) *Executor {
	return NewWithConcurrency(client, defaultConcurrency)
}

func NewWithConcurrency(client HTTPDoer, maxConcurrency int) *Executor {
	if client == nil {
		client = http.DefaultClient
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	return &Executor{client: client, maxConcurrency: maxConcurrency, now: time.Now}
}

// 接收Task → 解析输入 → 验证URL → 并发检查 → 统计结果 → 返回执行结果
func (e *Executor) Execute(ctx context.Context, task *domain.Task) (worker.ExecutionResult, error) {
	if task == nil {
		return worker.ExecutionResult{}, errors.New("task is nil")
	}

	var input Input
	if err := json.Unmarshal(task.Input, &input); err != nil {
		return worker.ExecutionResult{}, fmt.Errorf("decode URL check input: %w", err)
	}
	if len(input.URLs) == 0 {
		return worker.ExecutionResult{}, errors.New("at least one URL is required")
	}
	if len(input.URLs) > maxURLs {
		return worker.ExecutionResult{}, fmt.Errorf("URL count exceeds limit of %d", maxURLs)
	}

	output := Output{Total: len(input.URLs), Items: e.checkAll(ctx, input.URLs)}
	for _, item := range output.Items {
		if item.Error == "" {
			output.Succeeded++
		} else {
			output.Failed++
		}
	}

	payload, err := json.Marshal(output)
	if err != nil {
		return worker.ExecutionResult{}, fmt.Errorf("encode URL check output: %w", err)
	}

	result := worker.ExecutionResult{Output: payload, Outcome: worker.OutcomeSucceeded}
	if output.Failed > 0 {
		result.ErrorMessage = fmt.Sprintf("%d of %d URL checks failed", output.Failed, output.Total)
		result.Outcome = worker.OutcomePartial
	}
	if output.Succeeded == 0 {
		result.Outcome = worker.OutcomeFailed
	}
	return result, nil
}

type checkJob struct {
	index int
	url   string
}

func (e *Executor) checkAll(ctx context.Context, urls []string) []ItemResult {
	items := make([]ItemResult, len(urls))
	jobs := make(chan checkJob, len(urls))
	for index, rawURL := range urls {
		jobs <- checkJob{index: index, url: rawURL}
	}
	close(jobs)

	workerCount := min(e.maxConcurrency, len(urls))
	var waitGroup sync.WaitGroup
	waitGroup.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer waitGroup.Done()
			for job := range jobs {
				items[job.index] = e.check(ctx, job.url)
			}
		}()
	}
	waitGroup.Wait()
	return items
}

func (e *Executor) check(ctx context.Context, rawURL string) ItemResult {
	item := ItemResult{URL: rawURL}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		item.Error = "invalid HTTP URL"
		return item
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	request.Header.Set("User-Agent", "TaskPulse/0.1 URLCheck")

	startedAt := e.now()
	response, err := e.client.Do(request)
	item.DurationMS = e.now().Sub(startedAt).Milliseconds()
	if err != nil {
		item.Error = err.Error()
		return item
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseDrainSize))

	item.StatusCode = response.StatusCode
	item.FinalURL = response.Request.URL.String()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
		item.Error = fmt.Sprintf("unexpected HTTP status %d", response.StatusCode)
	}
	return item
}
