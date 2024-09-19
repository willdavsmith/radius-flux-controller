package controllers

import (
	"context"
	"fmt"
	"os"

	"encoding/json"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

// GitRepositoryWatcher watches GitRepository objects for revision changes
type GitRepositoryWatcher struct {
	client.Client
	artifactFetcher *fetch.ArchiveFetcher
	HttpRetry       int
}

func (r *GitRepositoryWatcher) SetupWithManager(mgr ctrl.Manager) error {
	r.artifactFetcher = fetch.New(
		fetch.WithRetries(r.HttpRetry),
		fetch.WithMaxDownloadSize(tar.UnlimitedUntarSize),
		fetch.WithUntar(tar.WithMaxUntarSize(tar.UnlimitedUntarSize)),
		fetch.WithHostnameOverwrite(os.Getenv("SOURCE_CONTROLLER_LOCALHOST")),
		fetch.WithLogger(nil),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&sourcev1.GitRepository{}, builder.WithPredicates(GitRepositoryRevisionChangePredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories/status,verbs=get

func (r *GitRepositoryWatcher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// get source object
	var repository sourcev1.GitRepository
	if err := r.Get(ctx, req.NamespacedName, &repository); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	artifact := repository.Status.Artifact
	log.Info("New revision detected", "revision", artifact.Revision)

	// create tmp dir
	tmpDir, err := os.MkdirTemp("", repository.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temp dir, error: %w", err)
	}

	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			log.Error(err, "unable to remove temp dir")
		}
	}(tmpDir)

	// download and extract artifact
	if err := r.artifactFetcher.Fetch(artifact.URL, artifact.Digest, tmpDir); err != nil {
		log.Error(err, "unable to fetch artifact")
		return ctrl.Result{}, err
	}

	// list artifact content
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list files, error: %w", err)
	}

	// TODO: Here's where we need to construct the "filesystem"
	for _, f := range files {
		log.Info("Processing " + f.Name())

		// print the file contents
		content, err := os.ReadFile(tmpDir + "/" + f.Name())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to read file, error: %w", err)
		}
		log.Info(string(content))

		escapedJSON := string(content)

		applicationDeployment := map[string]interface{}{
			"kind":       "ApplicationDeployment",
			"apiVersion": "radapp.io/v1alpha3",
			"metadata": map[string]string{
				"name":      "fluxdemo",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				// TODO: this should be ARM JSON, not Bicep
				"template": escapedJSON,
			},
		}

		// Marshal the ApplicationDeployment structure to get a properly escaped JSON string
		jsonOutput, err := json.Marshal(applicationDeployment)
		if err != nil {
			log.Error(err, "Error marshaling ApplicationDeployment to JSON")
			return ctrl.Result{}, err
		}

		// Use 'jsonOutput' as a byte slice. If you need it as a string, convert it using string(jsonOutput)
		log.Info("ApplicationDeployment JSON", "json", string(jsonOutput))

		// create or update the ApplicationDeployment object
		if _, err := applyApplicationDeployment(ctx, jsonOutput); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func applyApplicationDeployment(ctx context.Context, applicationDeployment []byte) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Create in-cluster client configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Error(err, "Failed to get in-cluster config")
		return ctrl.Result{}, err
	}

	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Error(err, "Failed to create dynamic client")
		return ctrl.Result{}, err
	}

	// Parse the JSON into an unstructured object
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(applicationDeployment), &obj); err != nil {
		log.Error(err, "Failed to unmarshal JSON into object")
		return ctrl.Result{}, err
	}

	// Convert the unstructured object into a dynamic object
	resource := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "radapp.io",
		Version:  "v1alpha3",
		Resource: "applicationdeployments",
	}).Namespace("default")

	// Attempt to get the object to see if it needs to be created or updated
	getObj, err := resource.Get(context.TODO(), "fluxdemo", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Object does not exist, so create it
		_, err = resource.Create(context.TODO(), &unstructured.Unstructured{
			Object: obj,
		}, metav1.CreateOptions{})
		if err != nil {
			log.Error(err, "Failed to create ApplicationDeployment object")
			return ctrl.Result{}, err
		}
	} else if err == nil {
		// Object exists, so update it
		unstructuredObj := &unstructured.Unstructured{
			Object: obj,
		}
		unstructuredObj.SetResourceVersion(getObj.GetResourceVersion())
		_, err = resource.Update(context.TODO(), unstructuredObj, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err, "Failed to update ApplicationDeployment object")
			return ctrl.Result{}, err
		}
	} else {
		// An error occurred that isn't due to the object not existing
		log.Error(err, "Failed to get ApplicationDeployment object")
		return ctrl.Result{}, err
	}

	log.Info("Successfully applied ApplicationDeployment object")
	return ctrl.Result{}, nil
}
