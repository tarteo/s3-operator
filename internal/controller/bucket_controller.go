/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"slices"

	core "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	s3v1 "github.com/onesteinbv/s3-operator/api/v1"
)

// BucketReconciler reconciles a Bucket object
type BucketReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type S3ClientConfig struct {
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
}

// +kubebuilder:rbac:groups=s3.onestein.nl,resources=buckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=s3.onestein.nl,resources=buckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=s3.onestein.nl,resources=buckets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Bucket object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *BucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("namespace", req.NamespacedName)
	log.Info("reconciling bucket")

	var bucket s3v1.Bucket
	if err := r.Get(ctx, req.NamespacedName, &bucket); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to find bucket")
		return ctrl.Result{}, err
	}
	const finalizer = "s3.onestein.nl/finalizer"

	// Add finalizer
	if bucket.ObjectMeta.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(&bucket, finalizer) {
		controllerutil.AddFinalizer(&bucket, finalizer)
		if err := r.Update(ctx, &bucket); err != nil {
			log.Error(err, "unable to update bucket")
			return ctrl.Result{}, err
		}
	}

	s3ClientConfig, err := r.getS3ClientConfig(&bucket)
	if err != nil {
		log.Error(err, "unable to get S3 config")
		return ctrl.Result{}, err
	}

	s3Client, err := r.getS3Client(s3ClientConfig)
	if err != nil {
		log.Error(err, "unable to create S3 client")
		return ctrl.Result{}, err
	}
	bucketHash, err := r.hashBucket(&bucket, s3ClientConfig)
	if err != nil {
		log.Error(err, "unable to create hash of bucket information")
		return ctrl.Result{}, err
	}
	bucketExists := r.bucketExists(s3Client, bucket.Spec.Name)

	if !bucket.ObjectMeta.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(&bucket, finalizer) {
		// Only delete the bucket if no resource exist for the bucket e.g. in case
		// multiple applications use the same bucket
		var buckets s3v1.BucketList
		if err := r.List(ctx, &buckets, client.MatchingFields{"status.hash": bucketHash}); err != nil {
			log.Error(err, "Unable to list buckets (filtering on status.hash)")
			return ctrl.Result{}, err
		}
		if len(buckets.Items) == 1 && bucketExists {
			switch bucket.Spec.DeletionPolicy {
			case s3v1.Always:
				log.Info("emptying bucket", "bucket", bucket.Spec.Name)
				if err := r.emptyBucket(s3Client, bucket.Spec.Name); err != nil {
					log.Error(err, "could not empty bucket", "bucket", bucket.Spec.Name)
					return ctrl.Result{}, err
				}
				log.Info("deleting bucket", "bucket", bucket.Spec.Name)
				if err := r.deleteBucket(s3Client, bucket.Spec.Name); err != nil {
					log.Error(err, "could not delete bucket (external resource)", "bucket", bucket.Spec.Name)
					return ctrl.Result{}, err
				}
			case s3v1.OnlyIfEmpty:
				bucketContainsObjects, err := r.containsObjects(s3Client, bucket.Spec.Name)
				if err != nil {
					log.Error(err, "could not assert if the bucket contain objects", "bucket", bucket.Spec.Name)
					return ctrl.Result{}, err
				}
				if !bucketContainsObjects {
					log.Info("deleting bucket", "bucket", bucket.Spec.Name)
					if err := r.deleteBucket(s3Client, bucket.Spec.Name); err != nil {
						log.Error(err, "could not delete bucket (external resource)", "bucket", bucket.Spec.Name)
						return ctrl.Result{}, err
					}
				} else {
					log.Info("preserving bucket due to deletion policy (DeletionPolicy = OnlyIfEmpty)", "bucket", bucket.Spec.Name)
				}
			case s3v1.Preserve:
				log.Info("preserving bucket due to deletion policy (DeletionPolicy = Preserve)", "bucket", bucket.Spec.Name)
			}
			bucketExists = false
		} else {
			log.Info("bucket is still used in another resource, not deleting", "bucket", bucket.Spec.Name)
		}

		controllerutil.RemoveFinalizer(&bucket, finalizer)
		if err := r.Update(ctx, &bucket); err != nil {
			log.Error(err, "unable to remove finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if !bucketExists {
		// Create
		if err := r.createBucket(s3Client, bucket.Spec.Name); err != nil {
			log.Error(err, "failed to create bucket (external resource)")
			return ctrl.Result{}, err
		}
		bucketExists = true
	}

	// Update status
	bucket.Status.Available = bucketExists
	bucket.Status.Hash = bucketHash
	if err := r.Status().Update(ctx, &bucket); err != nil {
		log.Error(err, "unable to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *BucketReconciler) getS3ClientConfig(bucket *s3v1.Bucket) (*S3ClientConfig, error) {
	var (
		region          string = os.Getenv("DEFAULT_S3_REGION")
		endpoint        string = os.Getenv("DEFAULT_S3_ENDPOINT")
		accessKey       string = os.Getenv("DEFAULT_S3_ACCESS_KEY")
		secretKey       string = os.Getenv("DEFAULT_S3_SECRET_KEY")
		validationError error
	)

	if bucket.Spec.Region != "" {
		region = bucket.Spec.Region
	}

	if bucket.Spec.Secret != "" {
		var secret core.Secret

		if err := r.Get(context.Background(), types.NamespacedName{Namespace: bucket.Namespace, Name: bucket.Spec.Secret}, &secret); err != nil {
			return nil, err
		}

		// Validate secret
		endpoint, validationError = r.getSecretData(&secret, bucket.Spec.EndpointKey)
		if validationError != nil {
			return nil, validationError
		}
		accessKey, validationError = r.getSecretData(&secret, bucket.Spec.AccessKey)
		if validationError != nil {
			return nil, validationError
		}
		secretKey, validationError = r.getSecretData(&secret, bucket.Spec.SecretKey)
		if validationError != nil {
			return nil, validationError
		}
	}

	return &S3ClientConfig{
		Region:    region,
		Endpoint:  endpoint,
		AccessKey: accessKey,
		SecretKey: secretKey,
	}, nil
}

func (r *BucketReconciler) getS3Client(clientConfig *S3ClientConfig) (*s3.S3, error) {
	config := aws.NewConfig().
		WithEndpoint(clientConfig.Endpoint).
		WithRegion(clientConfig.Region).
		WithCredentials(credentials.NewStaticCredentials(clientConfig.AccessKey, clientConfig.SecretKey, ""))
	session, err := session.NewSessionWithOptions(session.Options{Config: *config})
	if err != nil {
		return nil, err
	}
	return s3.New(session), nil
}

func (r *BucketReconciler) getSecretData(secret *core.Secret, key string) (string, error) {
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key `%s` not found in secret `%s`", key, secret.Name)
	}
	return string(value), nil
}

func (r *BucketReconciler) createBucket(s3Client *s3.S3, bucketName string) error {
	_, err := s3Client.CreateBucket(&s3.CreateBucketInput{Bucket: &bucketName})
	if err != nil {
		return err
	}
	s3Client.WaitUntilBucketExists(&s3.HeadBucketInput{Bucket: &bucketName})
	return nil
}

func (r *BucketReconciler) emptyBucket(s3Client *s3.S3, bucketName string) error {
	var maxKeys int64 = 1000
	var quiet bool = true
	var deletionError error
	err := s3Client.ListObjectsV2Pages(&s3.ListObjectsV2Input{Bucket: &bucketName, MaxKeys: &maxKeys}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		var objectsToDelete []*s3.ObjectIdentifier
		for _, obj := range page.Contents {
			objectsToDelete = append(objectsToDelete, &s3.ObjectIdentifier{Key: obj.Key})
		}
		if len(objectsToDelete) == 0 {
			return false
		}
		_, deletionError = s3Client.DeleteObjects(
			&s3.DeleteObjectsInput{
				Bucket: &bucketName,
				Delete: &s3.Delete{Quiet: &quiet, Objects: objectsToDelete},
			},
		)
		return deletionError == nil
	})

	if deletionError != nil {
		return deletionError
	}

	if err != nil {
		return err
	}
	return nil
}

func (r *BucketReconciler) deleteBucket(s3Client *s3.S3, bucketName string) error {
	_, err := s3Client.DeleteBucket(&s3.DeleteBucketInput{Bucket: &bucketName})
	if err != nil {
		return err
	}
	s3Client.WaitUntilBucketNotExists(&s3.HeadBucketInput{Bucket: &bucketName})
	return nil
}

func (r *BucketReconciler) bucketExists(s3Client *s3.S3, bucketName string) bool {
	_, err := s3Client.HeadBucket(&s3.HeadBucketInput{Bucket: &bucketName})
	return err == nil
}

func (r *BucketReconciler) containsObjects(s3Client *s3.S3, bucketName string) (bool, error) {
	var maxKeys int64 = 1
	res, err := s3Client.ListObjectsV2(&s3.ListObjectsV2Input{Bucket: &bucketName, MaxKeys: &maxKeys})
	if err != nil {
		return false, err
	}
	return *res.KeyCount > int64(0), nil
}

func (r *BucketReconciler) hashBucket(bucket *s3v1.Bucket, config *S3ClientConfig) (string, error) {
	if config == nil {
		var err error
		config, err = r.getS3ClientConfig(bucket)
		if err != nil {
			return "", err
		}
	}
	data := slices.Concat(
		[]byte(config.Endpoint),
		[]byte(config.AccessKey),
		[]byte(config.SecretKey),
		[]byte(bucket.Spec.Name),
	)
	return fmt.Sprintf("%x", sha1.Sum(data)), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), &s3v1.Bucket{}, "spec.name", func(obj client.Object) []string {
		resource := obj.(*s3v1.Bucket)
		return []string{resource.Spec.Name}
	}); err != nil {
		return err
	}

	if err := indexer.IndexField(context.Background(), &s3v1.Bucket{}, "status.hash", func(obj client.Object) []string {
		resource := obj.(*s3v1.Bucket)
		return []string{resource.Status.Hash}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&s3v1.Bucket{}).
		Named("bucket").
		Complete(r)
}
