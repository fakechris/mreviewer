package github

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type SnapshotFetcher[T any] interface {
	GetPullRequestSnapshot(ctx context.Context, owner, repo string, number int64) (T, error)
}

type Adapter[T any] struct {
	fetcher SnapshotFetcher[T]
}

func NewAdapter[T any](fetcher SnapshotFetcher[T]) *Adapter[T] {
	return &Adapter[T]{fetcher: fetcher}
}

func (a *Adapter[T]) FetchSnapshot(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.PlatformSnapshot, error) {
	if a == nil || a.fetcher == nil {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("github adapter: snapshot fetcher is required")
	}

	owner, repo, ok := strings.Cut(target.Repository, "/")
	if !ok || owner == "" || repo == "" {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("github adapter: target repository must be owner/repo")
	}

	snapshot, err := a.fetcher.GetPullRequestSnapshot(ctx, owner, repo, target.Number)
	if err != nil {
		return reviewcore.PlatformSnapshot{}, err
	}

	return snapshotFromValue(snapshot)
}

func snapshotFromValue[T any](snapshot T) (reviewcore.PlatformSnapshot, error) {
	value := reflect.ValueOf(snapshot)
	if !value.IsValid() {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("github adapter: empty snapshot")
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reviewcore.PlatformSnapshot{}, fmt.Errorf("github adapter: nil snapshot")
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("github adapter: snapshot must be a struct")
	}

	return reviewcore.PlatformSnapshot{
		Title:            stringField(value, "Title"),
		Description:      stringField(value, "Description"),
		SourceBranch:     stringField(value, "SourceBranch"),
		TargetBranch:     stringField(value, "TargetBranch"),
		BaseSHA:          stringField(value, "BaseSHA"),
		HeadSHA:          stringField(value, "HeadSHA"),
		RepositoryWebURL: stringField(value, "RepositoryWebURL"),
		Opaque:           snapshot,
	}, nil
}

func stringField(value reflect.Value, name string) string {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}
