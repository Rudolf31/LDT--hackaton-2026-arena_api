package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/revrost/go-openrouter"
)

// generationTemperature — низкая температура генерации документа: нужен
// предсказуемый структурированный ответ, не разнообразие формулировок
// (D-32).
const generationTemperature = 0.2

// transcriber — Transcriber через go-openrouter (D-32 уточняет D-13:
// библиотека используется для обеих моделей, хотя OpenRouter — роутер
// чат-моделей, а не специализированный STT-сервис). Адрес и ключ у
// расшифровки свои (ARENA_STT_*, по умолчанию — из ARENA_GEN_*, D-35).
type transcriber struct {
	client *openrouter.Client
	model  string
}

// NewTranscriber собирает клиента расшифровки. url и key уже проверены
// вызывающей стороной (cmd/portal/main.go вызывает это только когда
// cfg.STTConfigured()) — здесь их не перепроверяем.
func NewTranscriber(url, key, model string) Transcriber {
	cfg := openrouter.DefaultConfig(key)
	cfg.BaseURL = url
	return &transcriber{client: openrouter.NewClientWithConfig(*cfg), model: model}
}

func (t *transcriber) Transcribe(ctx context.Context, audio io.Reader) (string, error) {
	data, err := io.ReadAll(audio)
	if err != nil {
		return "", fmt.Errorf("чтение записи для расшифровки: %w", err)
	}
	resp, err := t.client.CreateTranscription(ctx, openrouter.TranscriptionRequest{
		Model:      t.model,
		InputAudio: openrouter.NewTranscriptionInputAudio(data, openrouter.AudioFormatMp3),
		Language:   "ru",
	})
	if err != nil {
		return "", classifyErr(ctx, err)
	}
	return resp.Text, nil
}

// generator — ScenarioGenerator через go-openrouter (D-13, D-32, D-33):
// системный промпт собирает и передаёт вызывающая сторона
// (generation/prompt.go) — этот клиент только отправляет чат-запрос с
// json_object и разбирает первый вариант ответа.
type generator struct {
	client       *openrouter.Client
	model        string
	systemPrompt string
}

// NewGenerator собирает клиента генерации документа. url, key и model уже
// проверены вызывающей стороной (cfg.GenConfigured()).
func NewGenerator(url, key, model, systemPrompt string) ScenarioGenerator {
	cfg := openrouter.DefaultConfig(key)
	cfg.BaseURL = url
	return &generator{client: openrouter.NewClientWithConfig(*cfg), model: model, systemPrompt: systemPrompt}
}

func (g *generator) Generate(ctx context.Context, brief string, base []byte, problems []string) ([]byte, error) {
	var user strings.Builder
	user.WriteString(brief)
	if base != nil {
		user.WriteString("\n\nТекущий документ сценария (JSON), который нужно изменить по указанию выше:\n")
		user.Write(base)
	}
	if len(problems) > 0 {
		user.WriteString("\n\nПрошлая попытка не прошла проверку. Исправь эти ошибки:\n")
		for _, p := range problems {
			user.WriteString("- ")
			user.WriteString(p)
			user.WriteString("\n")
		}
	}

	resp, err := g.client.CreateChatCompletion(ctx, openrouter.ChatCompletionRequest{
		Model: g.model,
		Messages: []openrouter.ChatCompletionMessage{
			{Role: openrouter.ChatMessageRoleSystem, Content: openrouter.Content{Text: g.systemPrompt}},
			{Role: openrouter.ChatMessageRoleUser, Content: openrouter.Content{Text: user.String()}},
		},
		ResponseFormat: &openrouter.ChatCompletionResponseFormat{
			Type: openrouter.ChatCompletionResponseFormatTypeJSONObject,
		},
		Temperature: generationTemperature,
	})
	if err != nil {
		return nil, classifyErr(ctx, err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%w: пустой ответ (нет вариантов)", ErrUpstream)
	}

	content := stripJSONFence(resp.Choices[0].Message.Content.Text)
	if !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("%w: ответ не JSON", ErrUpstream)
	}
	return []byte(content), nil
}

// stripJSONFence снимает ```json … ``` или ``` … ```, если модель обернула
// в них ответ, несмотря на response_format=json_object — некоторые модели
// это всё равно делают.
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// classifyErr сводит ошибку библиотеки к ErrTimeout/ErrUpstream — без
// текста ответа провайдера (там может быть эхо описания или указания,
// правило 5): только код состояния, если он был.
func classifyErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ErrTimeout
	}
	if status, ok := openrouter.HTTPStatusCode(err); ok {
		return fmt.Errorf("%w (код %d)", ErrUpstream, status)
	}
	return ErrUpstream
}
