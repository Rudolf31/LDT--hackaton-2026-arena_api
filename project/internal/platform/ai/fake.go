package ai

import (
	"context"
	"io"
)

// FakeTranscriber — двойник Transcriber для тестов generation (05-generation.md,
// «Тесты и проверка»): отвечает по очереди из Responses; когда вызовов
// больше, чем ответов, повторяется последний. Received хранит принятые
// байты каждого вызова — тестам «mp3 ушёл модели как есть» этого достаточно,
// не сохраняя сам звук нигде за пределами теста.
type FakeTranscriber struct {
	Responses []FakeTranscriberResponse
	Calls     int
	Received  [][]byte
}

type FakeTranscriberResponse struct {
	Text string
	Err  error
}

func (f *FakeTranscriber) Transcribe(_ context.Context, audio io.Reader) (string, error) {
	data, err := io.ReadAll(audio)
	if err != nil {
		return "", err
	}
	f.Received = append(f.Received, data)
	resp := f.responseAt(f.Calls)
	f.Calls++
	return resp.Text, resp.Err
}

func (f *FakeTranscriber) responseAt(call int) FakeTranscriberResponse {
	if len(f.Responses) == 0 {
		return FakeTranscriberResponse{}
	}
	if call < len(f.Responses) {
		return f.Responses[call]
	}
	return f.Responses[len(f.Responses)-1]
}

// FakeGenerator — двойник ScenarioGenerator: то же самое, плюс запись
// принятых brief/base/problems — тестам «ошибки ушли модели на повтор»
// нужно видеть, что во втором вызове problems не пуст.
type FakeGenerator struct {
	Responses []FakeGeneratorResponse
	Calls     int
	Requests  []FakeGeneratorRequest
}

type FakeGeneratorRequest struct {
	Brief    string
	Base     []byte
	Problems []string
}

type FakeGeneratorResponse struct {
	Document []byte
	Err      error
}

func (f *FakeGenerator) Generate(_ context.Context, brief string, base []byte, problems []string) ([]byte, error) {
	req := FakeGeneratorRequest{Brief: brief, Base: base}
	if len(problems) > 0 {
		req.Problems = append([]string{}, problems...)
	}
	f.Requests = append(f.Requests, req)
	resp := f.responseAt(f.Calls)
	f.Calls++
	return resp.Document, resp.Err
}

func (f *FakeGenerator) responseAt(call int) FakeGeneratorResponse {
	if len(f.Responses) == 0 {
		return FakeGeneratorResponse{}
	}
	if call < len(f.Responses) {
		return f.Responses[call]
	}
	return f.Responses[len(f.Responses)-1]
}

var (
	_ Transcriber       = (*FakeTranscriber)(nil)
	_ ScenarioGenerator = (*FakeGenerator)(nil)
)
