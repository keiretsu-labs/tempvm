package gateway

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func unstructuredString(object map[string]any, fields ...string) (string, bool, error) {
	return unstructured.NestedString(object, fields...)
}

func unstructuredSlice(object map[string]any, fields ...string) ([]any, bool, error) {
	return unstructured.NestedSlice(object, fields...)
}
