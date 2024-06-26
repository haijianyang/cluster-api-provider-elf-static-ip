/*
Copyright 2022.

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

package ipam

// IPAM labels.
const (
	ClusterNameKey        = "cluster.x-k8s.io/cluster-name"
	ClusterNetworkNameKey = "cluster.x-k8s.io/network-name"
	// group is used to identify the pool, for eg., 'dev/test/prod' or 'team1/team2'.
	ClusterIPPoolNameKey      = "cluster.x-k8s.io/ip-pool-name"
	ClusterIPPoolGroupKey     = "cluster.x-k8s.io/ip-pool-group"
	ClusterIPPoolNamespaceKey = "cluster.x-k8s.io/ip-pool-namespace"
	IPOwnerNameKey            = "cluster.x-k8s.io/ip-owner-name"
)

// Default IPPool.
const (
	DefaultIPPoolNamespace = "default"
	DefaultIPPoolKey       = "ippool.cluster.x-k8s.io/is-default"
)
