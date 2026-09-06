package model

type Limits struct {
	MaxArchiveBytes           int64 `json:"max_archive_bytes"`
	MaxExpandedBytes          int64 `json:"max_expanded_bytes"`
	MaxFileBytes              int64 `json:"max_file_bytes"`
	MaxFiles                  int   `json:"max_files"`
	MaxDepth                  int   `json:"max_depth"`
	AcquisitionTimeoutSeconds int   `json:"acquisition_timeout_seconds"`
	InspectionTimeoutSeconds  int   `json:"inspection_timeout_seconds"`
	OverallTimeoutSeconds     int   `json:"overall_timeout_seconds"`
}
type Request struct {
	WorkspaceID string `json:"workspace_id"`
	Repository  string `json:"repository"`
	CommitSHA   string `json:"commit_sha"`
	ArchiveURL  string `json:"archive_url"`
	Limits      Limits `json:"limits"`
}
type Artifact struct {
	Repository             string         `json:"repository"`
	CommitSHA              string         `json:"commit_sha"`
	RegularFiles           int            `json:"regular_file_count"`
	TotalBytes             int64          `json:"total_bytes"`
	Directories            int            `json:"directory_count"`
	MaximumDepth           int            `json:"maximum_depth"`
	Extensions             map[string]int `json:"extension_distribution"`
	Manifests              []string       `json:"manifests"`
	Ecosystems             []string       `json:"ecosystems"`
	CIConfiguration        bool           `json:"ci_configuration_present"`
	ContainerConfiguration bool           `json:"container_configuration_present"`
	Gitmodules             bool           `json:"gitmodules_present"`
	Symlinks               int            `json:"symlink_count"`
	SuspiciousEntries      int            `json:"suspicious_entry_count"`
	LargeFiles             int            `json:"large_file_count"`
	Truncated              bool           `json:"truncated"`
	DurationMilliseconds   int64          `json:"inspection_duration_ms"`
	SourceTrust            string         `json:"source_trust"`
}
type Result struct {
	WorkspaceID string    `json:"workspace_id"`
	Status      string    `json:"status"`
	Artifact    *Artifact `json:"inspection_artifact,omitempty"`
	FailureCode string    `json:"failure_code,omitempty"`
}
