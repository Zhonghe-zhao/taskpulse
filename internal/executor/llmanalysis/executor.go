package llmanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
)

type Executor struct {
	client Client
}

func New(client Client) (*Executor, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	return &Executor{client: client}, nil
}

func (e *Executor) Execute(ctx context.Context, task *domain.Task) (worker.ExecutionResult, error) {
	if task == nil {
		return worker.ExecutionResult{}, errors.New("task is nil")
	}

	var input Input
	if err := json.Unmarshal(task.Input, &input); err != nil {
		return worker.ExecutionResult{}, fmt.Errorf("decode LLM analysis input: %w", err)
	}
	request, err := buildRequest(input)
	if err != nil {
		return worker.ExecutionResult{}, NewInvalidPromptError(err)
	}

	response, err := e.client.Analyze(ctx, request)
	if err != nil {
		return worker.ExecutionResult{}, err
	}
	if strings.TrimSpace(response.Summary) == "" {
		return worker.ExecutionResult{}, NewInvalidPromptError(ErrEmptyLLMOutput)
	}

	output := Output{
		Subject: request.Subject,
		Summary: response.Summary,
		Plan:    append([]string(nil), response.Plan...),
		Model:   response.Model,
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return worker.ExecutionResult{}, fmt.Errorf("encode LLM analysis output: %w", err)
	}
	return worker.ExecutionResult{
		Output:  payload,
		Outcome: worker.OutcomeSucceeded,
	}, nil
}

func buildRequest(input Input) (AnalysisRequest, error) {
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		return AnalysisRequest{}, ErrEmptySubject
	}
	goal := strings.TrimSpace(input.Goal)
	if goal == "" {
		return AnalysisRequest{}, ErrEmptyGoal
	}
	notes := make([]string, 0, len(input.Notes))
	for _, note := range input.Notes {
		note = strings.TrimSpace(note)
		if note != "" {
			notes = append(notes, note)
		}
	}
	return AnalysisRequest{
		Subject: subject,
		Notes:   notes,
		Goal:    goal,
	}, nil
}
