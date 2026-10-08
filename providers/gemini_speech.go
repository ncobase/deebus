package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// SynthesizeSpeech calls generateContent with AUDIO output. Voice defaults to Kore and format to wav.
func (p *GeminiProvider) SynthesizeSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("speech request required")
	}
	format, mime, ok := geminiSpeechFormat(req.Format)
	if !ok {
		return nil, unsupportedCapability(p.Name(), "speech format "+req.Format)
	}
	parts, speechConfig, err := geminiSpeechParts(req)
	if err != nil {
		return nil, err
	}
	config := map[string]any{
		"responseModalities": []string{"AUDIO"},
		"speechConfig":       speechConfig,
	}
	if format != "" {
		config["responseFormat"] = map[string]any{
			"audio": map[string]any{"mimeType": format},
		}
	}
	body := map[string]any{
		"contents": []map[string]any{{
			"role":  "user",
			"parts": parts,
		}},
		"generationConfig": config,
	}
	payload, err := p.geminiContent(ctx, req.Model, body)
	if err != nil {
		return nil, err
	}
	var audio []byte
	mediaType := mime
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return nil, fmt.Errorf("decode gemini audio: %w", err)
			}
			audio = append(audio, decoded...)
			if part.InlineData.MIMEType != "" {
				mediaType = part.InlineData.MIMEType
			}
		}
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("gemini returned no audio")
	}
	return &SpeechResponse{Audio: audio, MediaType: mediaType, Model: req.Model, Provider: p.Name()}, nil
}

// Transcribe sends audio inline to generateContent and returns the text parts.
func (p *GeminiProvider) Transcribe(ctx context.Context, req *TranscribeRequest) (*TranscribeResponse, error) {
	if req == nil || len(req.Data) == 0 {
		return nil, fmt.Errorf("audio data required")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Transcribe this audio verbatim."
	}
	if req.Language != "" {
		prompt += " Language: " + req.Language + "."
	}
	mediaType := req.MediaType
	if mediaType == "" {
		mediaType = "audio/mpeg"
	}
	body := map[string]any{
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]any{
				{"inlineData": map[string]string{
					"mimeType": mediaType,
					"data":     base64.StdEncoding.EncodeToString(req.Data),
				}},
				{"text": prompt},
			},
		}},
	}
	payload, err := p.geminiContent(ctx, req.Model, body)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
		}
	}
	if text.Len() == 0 {
		return nil, fmt.Errorf("gemini returned no transcript")
	}
	return &TranscribeResponse{Text: text.String(), Model: req.Model, Provider: p.Name()}, nil
}

type geminiContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (p *GeminiProvider) geminiContent(ctx context.Context, model string, body any) (*geminiContentResponse, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("gemini model required")
	}
	var payload geminiContentResponse
	path := "/v1beta/models/" + model + ":generateContent"
	if err := p.postGemini(ctx, path, body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func geminiSpeechParts(req *SpeechRequest) ([]map[string]any, map[string]any, error) {
	if len(req.Speakers) > 0 {
		parts := make([]map[string]any, 0, len(req.Speakers))
		voices := make([]map[string]any, 0, len(req.Speakers))
		seen := map[string]struct{}{}
		for i, turn := range req.Speakers {
			if strings.TrimSpace(turn.Text) == "" {
				return nil, nil, fmt.Errorf("speaker %d text required", i)
			}
			speaker := turn.Speaker
			if speaker == "" {
				speaker = fmt.Sprintf("Speaker%d", i+1)
			}
			meta := map[string]any{"speaker": speaker}
			if turn.Style != "" {
				meta["style"] = turn.Style
			}
			parts = append(parts, map[string]any{"text": turn.Text, "speech_metadata": meta})
			if _, ok := seen[speaker]; ok {
				continue
			}
			seen[speaker] = struct{}{}
			voice := turn.Voice
			if voice == "" {
				voice = req.Voice
			}
			if voice == "" {
				return nil, nil, fmt.Errorf("speaker %q voice required", speaker)
			}
			voices = append(voices, map[string]any{
				"speaker": speaker,
				"voiceConfig": map[string]any{
					"prebuiltVoiceConfig": map[string]string{"voiceName": voice},
				},
			})
		}
		config := map[string]any{
			"multiSpeakerVoiceConfig": map[string]any{"speakerVoiceConfigs": voices},
		}
		if req.Language != "" {
			config["languageCode"] = req.Language
		}
		return parts, config, nil
	}
	if strings.TrimSpace(req.Input) == "" {
		return nil, nil, fmt.Errorf("speech input required")
	}
	voice := req.Voice
	if voice == "" {
		voice = "Kore"
	}
	part := map[string]any{"text": req.Input}
	if req.Instructions != "" {
		part["speech_metadata"] = map[string]any{"style": req.Instructions}
	}
	config := map[string]any{
		"voiceConfig": map[string]any{"voice": voice},
	}
	if req.Language != "" {
		config["languageCode"] = req.Language
	}
	return []map[string]any{part}, config, nil
}

func geminiSpeechFormat(format string) (apiFormat, mediaType string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "wav":
		return "AUDIO_WAV", "audio/wav", true
	case "pcm", "l16":
		return "AUDIO_L16", "audio/l16", true
	case "mulaw":
		return "AUDIO_MULAW", "audio/basic", true
	case "alaw":
		return "AUDIO_ALAW", "audio/alaw", true
	default:
		return "", "", false
	}
}
