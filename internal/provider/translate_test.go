package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslateState(t *testing.T) {
	tests := []struct {
		name    string
		input   []map[string]interface{}
		want    []map[string]interface{}
		wantErr string
	}{
		{
			name: "deployment with namespace, status, replicas",
			input: []map[string]interface{}{
				{
					"resource":  "deployment/payment-service",
					"namespace": "default",
					"status":    "running",
					"replicas":  3,
				},
			},
			want: []map[string]interface{}{
				{
					"kind":      "deployment",
					"name":      "payment-service",
					"namespace": "default",
					"spec": map[string]interface{}{
						"status":   "running",
						"replicas": 3,
					},
				},
			},
		},
		{
			name: "namespace with zone",
			input: []map[string]interface{}{
				{
					"resource": "namespace/frontend",
					"zone":     "zone-a",
				},
			},
			want: []map[string]interface{}{
				{
					"kind": "namespace",
					"name": "frontend",
					"zone": "zone-a",
				},
			},
		},
		{
			name: "configmap with data map",
			input: []map[string]interface{}{
				{
					"resource":  "configmap/smtp-config",
					"namespace": "default",
					"data":      map[string]interface{}{"SMTP_HOST": "smtp.internal"},
				},
			},
			want: []map[string]interface{}{
				{
					"kind":      "configmap",
					"name":      "smtp-config",
					"namespace": "default",
					"data":      map[string]interface{}{"SMTP_HOST": "smtp.internal"},
				},
			},
		},
		{
			name: "secret with data map",
			input: []map[string]interface{}{
				{
					"resource":  "secret/db-credentials",
					"namespace": "default",
					"data":      map[string]interface{}{"password": "hunter2"},
				},
			},
			want: []map[string]interface{}{
				{
					"kind":      "secret",
					"name":      "db-credentials",
					"namespace": "default",
					"data":      map[string]interface{}{"password": "hunter2"},
				},
			},
		},
		{
			name: "service with selector in spec",
			input: []map[string]interface{}{
				{
					"resource": "service/api-gateway",
					"selector": map[string]interface{}{"app": "api"},
				},
			},
			want: []map[string]interface{}{
				{
					"kind": "service",
					"name": "api-gateway",
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{"app": "api"},
					},
				},
			},
		},
		{
			name: "labels and annotations preserved at top level",
			input: []map[string]interface{}{
				{
					"resource":    "deployment/web-app",
					"namespace":   "default",
					"labels":      map[string]interface{}{"app": "web"},
					"annotations": map[string]interface{}{"version": "v2"},
					"replicas":    3,
				},
			},
			want: []map[string]interface{}{
				{
					"kind":        "deployment",
					"name":        "web-app",
					"namespace":   "default",
					"labels":      map[string]interface{}{"app": "web"},
					"annotations": map[string]interface{}{"version": "v2"},
					"spec": map[string]interface{}{
						"replicas": 3,
					},
				},
			},
		},
		{
			name: "missing resource field",
			input: []map[string]interface{}{
				{
					"namespace": "default",
					"status":    "running",
				},
			},
			wantErr: `state entry 0: missing required field "resource"`,
		},
		{
			name: "resource field without separator",
			input: []map[string]interface{}{
				{
					"resource": "deployment-no-slash",
				},
			},
			wantErr: `state entry 0: "resource" field "deployment-no-slash" must be in "kind/name" format`,
		},
		{
			name: "multiple entries translated",
			input: []map[string]interface{}{
				{"resource": "namespace/frontend", "zone": "zone-a"},
				{"resource": "deployment/web-app", "namespace": "frontend", "replicas": 3},
			},
			want: []map[string]interface{}{
				{"kind": "namespace", "name": "frontend", "zone": "zone-a"},
				{
					"kind":      "deployment",
					"name":      "web-app",
					"namespace": "frontend",
					"spec":      map[string]interface{}{"replicas": 3},
				},
			},
		},
		{
			name:  "empty state",
			input: []map[string]interface{}{},
			want:  []map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TranslateState(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
