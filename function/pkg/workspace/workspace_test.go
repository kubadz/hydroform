package workspace

import (
	"bytes"
	"context"
	"errors"
	"github.com/golang/mock/gomock"
	"github.com/kyma-incubator/hydroform/function/pkg/client"
	mockclient "github.com/kyma-incubator/hydroform/function/pkg/client/automock"
	"io"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"reflect"
	"testing"

	"github.com/kyma-incubator/hydroform/function/pkg/resources/types"
)

func Test_workspace_build(t *testing.T) {
	type args struct {
		cfg            Cfg
		dirPath        string
		writerProvider WriterProvider
	}
	tests := []struct {
		name    string
		ws      workspace
		args    args
		wantErr bool
	}{
		{
			name:    "write error",
			wantErr: true,
			args: args{
				writerProvider: func() WriterProvider {
					return func(path string) (io.Writer, Cancel, error) {
						return &errWriter{}, nil, nil
					}
				}(),
			},
		},
		{
			name:    "happy path",
			wantErr: false,
			args: args{
				writerProvider: func(path string) (io.Writer, Cancel, error) {
					return &bytes.Buffer{}, nil, nil
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.ws.build(tt.args.cfg, tt.args.dirPath, tt.args.writerProvider); (err != nil) != tt.wantErr {
				t.Errorf("workspace.build() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_initialize(t *testing.T) {
	type args struct {
		cfg            Cfg
		dirPath        string
		writerProvider WriterProvider
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name:    "unsupported runtime",
			wantErr: true,
			args: args{
				cfg: Cfg{
					Runtime: types.Runtime("unsupported runtime"),
				},
			},
		},
		{
			name:    "happy path",
			wantErr: false,
			args: args{
				cfg: Cfg{
					Runtime: types.Python38,
					Triggers: []Trigger{
						{
							Version: "test-version",
							Source:  "test-source",
							Type:    "test-type",
						},
					},
				},
				writerProvider: func(path string) (io.Writer, Cancel, error) {
					return &bytes.Buffer{}, nil, nil
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := initialize(tt.args.cfg, tt.args.dirPath, tt.args.writerProvider); (err != nil) != tt.wantErr {
				t.Errorf("initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_fromRuntime(t *testing.T) {
	type args struct {
		runtime types.Runtime
	}
	tests := []struct {
		name    string
		args    args
		want    workspace
		wantErr bool
	}{
		{
			name: "unsupported runtime error",
			args: args{
				runtime: types.Runtime("unsupported"),
			},
			wantErr: true,
		},
		{
			name: "nodejs10",
			args: args{
				runtime: types.Nodejs10,
			},
			want:    workspaceNodeJs,
			wantErr: false,
		},
		{
			name: "nodejs12",
			args: args{
				runtime: types.Nodejs12,
			},
			want:    workspaceNodeJs,
			wantErr: false,
		},
		{
			name: "python38",
			args: args{
				runtime: types.Python38,
			},
			want:    workspacePython,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fromRuntime(tt.args.runtime)
			if (err != nil) != tt.wantErr {
				t.Errorf("fromRuntime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromRuntime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_Synchronise(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	type args struct {
		ctx        context.Context
		cfg        Cfg
		outputPath string
		build      client.Build
	}

	name := "test"
	namespace := "test-ns"

	tests := []struct {
		name    string
		args    args
		want    workspace
		wantErr bool
	}{
		{
			name:    "getting function should fail",
			wantErr: true,
			args: args{
				build: func(namespace string, resource schema.GroupVersionResource) client.Client {
					result := mockclient.NewMockClient(ctrl)

					result.EXPECT().
						Get(nil, "", v1.GetOptions{}).
						Return(nil, errors.New("")).
						Times(1)

					return result
				},
			},
		},
		{
			name:    "getting triggers as unstructured list should fail",
			wantErr: true,
			args: args{
				cfg: Cfg{
					Name:      name,
					Namespace: namespace,
				},
				build: func() client.Build {

					result := mockclient.NewMockClient(ctrl)

					result.EXPECT().
						Get(gomock.Any(), name, v1.GetOptions{}).
						Return(&unstructured.Unstructured{Object: map[string]interface{}{"test": "test"}}, nil).
						Times(1)

					result.EXPECT().
						List(gomock.Any(), v1.ListOptions{LabelSelector: "ownerID="}).
						Return(&unstructured.UnstructuredList{}, errors.New("the error")).
						Times(1)

					return func(_ string, _ schema.GroupVersionResource) client.Client {
						return result
					}
				}(),
				ctx: context.Background(),
			},
		},
		{
			name: "inline happy path with triggers",
			args: args{
				cfg: Cfg{
					Name:      name,
					Namespace: namespace,
					Runtime:   types.Nodejs12,
					Source: Source{
						Type: SourceTypeInline,
						SourceInline: SourceInline{
							SourcePath:        "./testdir/inline",
							SourceHandlerName: handlerJs,
							DepsHandlerName:   packageJSON,
						},
					},
					Resources: Resources{
						Limits:   nil,
						Requests: nil,
					},
					Triggers: []Trigger{
						{
							Version: "v1.0.0",
							Source:  "the-source",
							Type:    "t1",
						},
					},
				},
				build: func() client.Build {
					c := inlineClient(ctrl, name, namespace)
					return func(_ string, _ schema.GroupVersionResource) client.Client {
						return c
					}
				}(),
			},
			wantErr: false,
		},
		{
			name: "gitrepo happy path",
			args: args{
				cfg: Cfg{
					Name:      name,
					Namespace: namespace,
					Runtime:   types.Nodejs12,
					Source: Source{
						Type: SourceTypeGit,
						SourceGit: SourceGit{
							URL:       "https://test.com",
							Reference: "master",
							BaseDir:   "/",
						},
					},
					Resources: Resources{
						Limits:   nil,
						Requests: nil,
					},
					Triggers: []Trigger{
						{
							Version: "v1.0.0",
							Source:  "the-source",
							Type:    "t1",
						},
					},
				},
				build: func() client.Build {
					c := gitClient(ctrl, name, namespace)
					return func(_ string, _ schema.GroupVersionResource) client.Client {
						return c
					}
				}(),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := synchronise(tt.args.ctx, tt.args.cfg, tt.args.outputPath, tt.args.build, newStrWriterProvider())
			if (err != nil) != tt.wantErr {
				t.Errorf("Synchronise() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func newStrWriterProvider() WriterProvider {
	return func(path string) (io.Writer, Cancel, error) {
		var buffer bytes.Buffer
		return &buffer, func() error {
			return nil
		}, nil
	}
}

func Test_fromSources(t *testing.T) {
	type args struct {
		runtime types.Runtime
		source  string
		deps    string
	}
	tests := []struct {
		name    string
		args    args
		want    workspace
		wantErr bool
	}{
		{
			name: "unsupported runtime error",
			args: args{
				runtime: "unsupported",
				source:  "",
				deps:    "",
			},
			want:    workspace{},
			wantErr: true,
		},
		{
			name: "nodejs10",
			args: args{
				runtime: types.Nodejs10,
				source:  handlerJs,
				deps:    packageJSON,
			},
			want:    workspaceNodeJs,
			wantErr: false,
		},
		{
			name: "nodejs12",
			args: args{
				runtime: types.Nodejs12,
				source:  handlerJs,
				deps:    packageJSON,
			},
			want:    workspaceNodeJs,
			wantErr: false,
		},
		{
			name: "python38",
			args: args{
				runtime: types.Python38,
				source:  handlerPython,
				deps:    "deps",
			},
			want: workspace{
				newTemplatedFile(handlerPython, FileNameHandlerPy),
				newTemplatedFile("deps", FileNameRequirementsTxt),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fromSources(tt.args.runtime, tt.args.source, tt.args.deps)
			if (err != nil) != tt.wantErr {
				t.Errorf("fromSources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromSources() = %v, want %v", got, tt.want)
			}
		})
	}
}

func inlineClient(ctrl *gomock.Controller, name, namespace string) client.Client {
	result := mockclient.NewMockClient(ctrl)

	result.EXPECT().
		Get(gomock.Any(), name, v1.GetOptions{}).
		Return(&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "serverless.kyma-project.io/v1alpha1",
			"kind":       "Function",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"maxReplicas": 1,
				"minReplicas": 1,
				"resources": map[string]interface{}{
					"limits": map[string]interface{}{
						"cpu":    "100m",
						"memory": "128Mi",
					},
				},
				"runtime": "nodejs12",
				"source":  handlerJs,
				"deps":    packageJSON,
			},
		}}, nil).Times(1)

	result.EXPECT().
		List(gomock.Any(), v1.ListOptions{LabelSelector: "ownerID="}).Return(&unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			{
				Object: map[string]interface{}{
					"trigger": map[string]interface{}{
						"spec": map[string]interface{}{
							"filter": map[string]interface{}{
								"attributes": map[string]interface{}{
									"eventtypeversion": "v1.0.0",
									"source":           "the-source",
									"type":             "t1",
								},
							},
						},
					},
				},
			},
		},
	}, nil).Times(1)

	return result
}

func gitClient(ctrl *gomock.Controller, name, namespace string) client.Client {
	result := mockclient.NewMockClient(ctrl)

	result.EXPECT().
		Get(gomock.Any(), name, v1.GetOptions{}).
		Return(&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "serverless.kyma-project.io/v1alpha1",
			"kind":       "Function",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"maxReplicas": 1,
				"minReplicas": 1,
				"resources": map[string]interface{}{
					"limits": map[string]interface{}{
						"cpu":    "100m",
						"memory": "128Mi",
					},
				},
				"runtime": "nodejs12",
				"source":  handlerJs,
				"deps":    packageJSON,
				"type":    "git",
			},
		}}, nil).Times(1)

	result.EXPECT().
		List(gomock.Any(), v1.ListOptions{LabelSelector: "ownerID="}).
		Return(&unstructured.UnstructuredList{}, nil).
		Times(1)

	result.EXPECT().Get(gomock.Any(), name, v1.GetOptions{}).Return(&unstructured.Unstructured{
		Object: map[string]interface{}{
			"gitrepository": map[string]interface{}{
				"spec": map[string]interface{}{
					"url": "http://test.com",
				},
			},
		}}, nil).Times(1)

	return result
}
