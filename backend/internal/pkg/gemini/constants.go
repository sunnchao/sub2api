package gemini

// Gemini API 端点
const (
	// Google AI Studio API 端点
	AIStudioBaseURL = "https://generativelanguage.googleapis.com"

	// API 版本
	APIVersion   = "v1beta"
	APIVersionV1 = "v1"
)

// Model represents a Gemini model
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name"`
}

// DefaultModels Gemini models list
var DefaultModels = []Model{
	{ID: "gemini-2.5-pro", Object: "model", Created: 1735084800, OwnedBy: "google", DisplayName: "Gemini 2.5 Pro"},
	{ID: "gemini-2.5-flash", Object: "model", Created: 1735084800, OwnedBy: "google", DisplayName: "Gemini 2.5 Flash"},
	{ID: "gemini-2.0-flash", Object: "model", Created: 1733875200, OwnedBy: "google", DisplayName: "Gemini 2.0 Flash"},
	{ID: "gemini-2.0-flash-thinking-exp", Object: "model", Created: 1733875200, OwnedBy: "google", DisplayName: "Gemini 2.0 Flash Thinking"},
	{ID: "gemini-1.5-pro", Object: "model", Created: 1715126400, OwnedBy: "google", DisplayName: "Gemini 1.5 Pro"},
	{ID: "gemini-1.5-flash", Object: "model", Created: 1715126400, OwnedBy: "google", DisplayName: "Gemini 1.5 Flash"},
}

// DefaultModelIDs returns the default model ID list
func DefaultModelIDs() []string {
	ids := make([]string, len(DefaultModels))
	for i, m := range DefaultModels {
		ids[i] = m.ID
	}
	return ids
}

// DefaultTestModel default model for testing Gemini accounts
const DefaultTestModel = "gemini-2.0-flash"

// BuildGenerateContentURL builds the generateContent API URL
func BuildGenerateContentURL(baseURL, model string, stream bool) string {
	if baseURL == "" {
		baseURL = AIStudioBaseURL
	}
	endpoint := "generateContent"
	if stream {
		endpoint = "streamGenerateContent"
	}
	return baseURL + "/" + APIVersion + "/models/" + model + ":" + endpoint
}

// BuildVertexAIURL builds the Vertex AI generateContent API URL
func BuildVertexAIURL(region, projectID, model string, stream bool) string {
	endpoint := "generateContent"
	if stream {
		endpoint = "streamGenerateContent"
	}
	return "https://" + region + "-aiplatform.googleapis.com/v1/projects/" + projectID +
		"/locations/" + region + "/publishers/google/models/" + model + ":" + endpoint
}
