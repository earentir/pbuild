package provenance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	StatementType  = "https://in-toto.io/Statement/v1"
	PredicateType  = "https://slsa.dev/provenance/v1"
	BuildTypeURI   = "https://github.com/earentir/pbuild/slsa-buildtype/v1"
	ProvenanceFile = "provenance.intoto.jsonl"
)

// Artifact describes a built artifact with digests for the subject.
type Artifact struct {
	Name   string
	SHA256 string
	SHA512 string
}

// Inputs holds all data needed to generate SLSA provenance for one build.
type Inputs struct {
	VersionDir       string
	InvocationID     string
	StartedOn        time.Time
	FinishedOn       time.Time
	ProjectName      string
	VersionTag       string
	Hostname         string
	Username         string
	GoVersion        string
	PbuildVersion    string
	ExternalParams   map[string]interface{}
	InternalParams   map[string]interface{}
	Targets          []Target
	Artifacts        []Artifact
}

// Target is a single OS/arch target (avoids importing targets package).
type Target struct {
	OS   string
	Arch string
}

// in-toto Statement (SLSA provenance)
type statement struct {
	Type          string     `json:"_type"`
	Subject       []subject  `json:"subject"`
	PredicateType string     `json:"predicateType"`
	Predicate     *predicate `json:"predicate"`
}

type subject struct {
	Name   string            `json:"name,omitempty"`
	Digest map[string]string `json:"digest"`
}

type predicate struct {
	BuildDefinition buildDefinition `json:"buildDefinition"`
	RunDetails      runDetails     `json:"runDetails"`
}

type buildDefinition struct {
	BuildType            string                 `json:"buildType"`
	ExternalParameters   map[string]interface{} `json:"externalParameters"`
	InternalParameters   map[string]interface{} `json:"internalParameters"`
	ResolvedDependencies []resourceDescriptor  `json:"resolvedDependencies,omitempty"`
}

type runDetails struct {
	Builder    builder             `json:"builder"`
	Metadata   metadata            `json:"metadata"`
	Byproducts []resourceDescriptor `json:"byproducts,omitempty"`
}

type builder struct {
	ID string `json:"id"`
}

type metadata struct {
	InvocationID string `json:"invocationId"`
	StartedOn    string `json:"startedOn"`
	FinishedOn   string `json:"finishedOn"`
}

type resourceDescriptor struct {
	URI    string            `json:"uri,omitempty"`
	Digest map[string]string `json:"digest,omitempty"`
	Name   string            `json:"name,omitempty"`
}

// Write generates provenance.intoto.jsonl in versionDir with one Statement per artifact.
// Each line is a complete JSON Statement (same predicate; subject is the artifact).
func Write(in *Inputs) (string, error) {
	if len(in.Artifacts) == 0 {
		return "", nil
	}

	builderID := "https://github.com/earentir/pbuild@" + in.PbuildVersion
	if in.Hostname != "" {
		builderID += "?host=" + in.Hostname
	}

	startedStr := in.StartedOn.UTC().Format(time.RFC3339)
	finishedStr := in.FinishedOn.UTC().Format(time.RFC3339)

	pred := &predicate{
		BuildDefinition: buildDefinition{
			BuildType:          BuildTypeURI,
			ExternalParameters: in.ExternalParams,
			InternalParameters: in.InternalParams,
		},
		RunDetails: runDetails{
			Builder: builder{ID: builderID},
			Metadata: metadata{
				InvocationID: in.InvocationID,
				StartedOn:    startedStr,
				FinishedOn:   finishedStr,
			},
		},
	}

	path := filepath.Join(in.VersionDir, ProvenanceFile)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create provenance file: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	for _, a := range in.Artifacts {
		if a.SHA256 == "" {
			continue
		}
		digest := map[string]string{"sha256": a.SHA256}
		if a.SHA512 != "" {
			digest["sha512"] = a.SHA512
		}
		stmt := statement{
			Type:          StatementType,
			Subject:       []subject{{Name: a.Name, Digest: digest}},
			PredicateType: PredicateType,
			Predicate:     pred,
		}
		if err := enc.Encode(&stmt); err != nil {
			return "", fmt.Errorf("write statement: %w", err)
		}
	}

	return path, nil
}
