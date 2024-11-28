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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// +kubebuilder:validation:Enum=Always;OnlyIfEmpty;Preserve
type DeletionPolicy string

const (
	Always      DeletionPolicy = "Always"
	OnlyIfEmpty DeletionPolicy = "OnlyIfEmpty"
	Preserve    DeletionPolicy = "Preserve"
)

// BucketSpec defines the desired state of Bucket.
type BucketSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Name is immutable"
	// Name of the bucket
	Name string `json:"name"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	// +optional
	Region string `json:"region"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Secret is immutable"
	// +optional
	// Name of the secret contains the access and secret key
	Secret string `json:"secret,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="AccessKey is immutable"
	// +kubebuilder:validation:Immutable=true
	// +optional
	// The key within the secret that contains the S3 access key
	AccessKey string `json:"accessKey"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SecretKey is immutable"
	// +kubebuilder:default:="secretKey"
	// +optional
	// The key within the secret that contains the S3 secret key
	SecretKey string `json:"secretKey"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="EndpointKey is immutable"
	// +kubebuilder:default:="endpointKey"
	// +optional
	// The key within the secret that contains the endpoint
	EndpointKey string `json:"endpointKey"`

	// Determines what should happen with the bucket if the resource is deleted
	// Valid values are:
	// - "Always" (default): Deletes the bucket even if it contains objects;
	// - "OnlyIfEmpty": Only delete bucket if is has no objects
	// - "Preserve": Never delete the bucket
	// +kubebuilder:default:="Always"
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy"`
}

// BucketStatus defines the observed state of Bucket.
type BucketStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Available bool   `json:"available"`
	Hash      string `json:"hash"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Bucket is the Schema for the buckets API.
type Bucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BucketSpec   `json:"spec,omitempty"`
	Status BucketStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BucketList contains a list of Bucket.
type BucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bucket `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bucket{}, &BucketList{})
}
