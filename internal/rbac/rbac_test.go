package rbac

import (
	"context"
	"reflect"
	"testing"
)

type recordingRepository struct {
	roles []string
}

func (r *recordingRepository) ReplaceUserRoles(_ context.Context, _ uint64, roles []string) error {
	r.roles = append([]string(nil), roles...)
	return nil
}

func (r *recordingRepository) UserRoles(_ context.Context, _ uint64) ([]string, error) {
	return append([]string(nil), r.roles...), nil
}

func (r *recordingRepository) HasPermission(_ context.Context, _ uint64, _ string) (bool, error) {
	return false, nil
}

func TestServiceReplaceUserRolesNormalizesSet(t *testing.T) {
	repository := &recordingRepository{}
	service := NewService(repository)
	if err := service.ReplaceUserRoles(context.Background(), 1, []string{"customer", " admin ", "customer"}); err != nil {
		t.Fatalf("ReplaceUserRoles() error: %v", err)
	}
	want := []string{"admin", "customer"}
	if !reflect.DeepEqual(repository.roles, want) {
		t.Fatalf("roles=%v, want %v", repository.roles, want)
	}
}
