/*
Copyright © 2022 The OpenCli Authors

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

package dbcluster

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/dynamic"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/describe"

	"jihulab.com/infracreate/dbaas-system/opencli/pkg/cmd/playground"
	"jihulab.com/infracreate/dbaas-system/opencli/pkg/types"
	"jihulab.com/infracreate/dbaas-system/opencli/pkg/utils"
)

type DescribeOptions struct {
	Namespace string

	Describer  func(*meta.RESTMapping) (describe.ResourceDescriber, error)
	NewBuilder func() *resource.Builder

	BuilderArgs []string

	EnforceNamespace bool
	AllNamespaces    bool

	DescriberSettings *describe.DescriberSettings
	FilenameOptions   *resource.FilenameOptions

	client dynamic.Interface
	genericclioptions.IOStreams
}

func NewDescribeCmd(f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	o := &DescribeOptions{
		FilenameOptions: &resource.FilenameOptions{},
		DescriberSettings: &describe.DescriberSettings{
			ShowEvents: true,
		},

		IOStreams: streams,
	}

	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe database cluster info",
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(o.Complete(f, args))
			cmdutil.CheckErr(o.Run())
		},
	}

	return cmd
}

func (o *DescribeOptions) Complete(f cmdutil.Factory, args []string) error {
	var err error
	if len(args) == 0 {
		return errors.New("You must specify the database cluster name to describe.")
	}

	o.Namespace, o.EnforceNamespace, err = f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}

	if o.AllNamespaces {
		o.EnforceNamespace = false
	}

	o.BuilderArgs = append([]string{types.PlaygroundSourceName}, args...)

	o.Describer = func(mapping *meta.RESTMapping) (describe.ResourceDescriber, error) {
		return describe.DescriberFn(f, mapping)
	}

	// used to fetch the resource
	config, err := f.ToRESTConfig()
	if err != nil {
		return nil
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}

	o.client = client
	o.NewBuilder = f.NewBuilder

	return nil
}

func (o *DescribeOptions) Run() error {
	r := o.NewBuilder().
		Unstructured().
		ContinueOnError().
		NamespaceParam(o.Namespace).DefaultNamespace().AllNamespaces(o.AllNamespaces).
		FilenameParam(o.EnforceNamespace, o.FilenameOptions).
		ResourceTypeOrNameArgs(true, o.BuilderArgs...).
		RequestChunksOf(o.DescriberSettings.ChunkSize).
		Flatten().
		Do()
	err := r.Err()
	if err != nil {
		return err
	}

	var allErrs []error
	infos, err := r.Infos()
	if err != nil {
		return err
	}

	errs := sets.NewString()
	for _, info := range infos {
		clusterInfo := utils.DBClusterInfo{
			RootUser: playground.DefaultRootUser,
			DBPort:   playground.DefaultPort,
		}

		mapping := info.ResourceMapping()
		if err != nil {
			if errs.Has(err.Error()) {
				continue
			}
			allErrs = append(allErrs, err)
			errs.Insert(err.Error())
			continue
		}

		clusterInfo.DBNamespace = info.Namespace
		clusterInfo.DBCluster = info.Name
		obj, err := o.client.Resource(mapping.Resource).Namespace(o.Namespace).Get(context.TODO(), info.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		buildClusterInfo(obj, &clusterInfo)
		utils.PrintClusterInfo(clusterInfo)
	}

	if len(infos) == 0 && len(allErrs) == 0 {
		// if we wrote no output, and had no errors, be sure we output something.
		if o.AllNamespaces {
			fmt.Fprintln(o.ErrOut, "No resources found")
		} else {
			fmt.Fprintf(o.ErrOut, "No resources found in %s namespace.\n", o.Namespace)
		}
	}

	return utilerrors.NewAggregate(allErrs)
}

func buildClusterInfo(obj *unstructured.Unstructured, info *utils.DBClusterInfo) {
	for k, v := range obj.GetLabels() {
		info.Labels = info.Labels + fmt.Sprintf("%s:%s ", k, v)
	}

	status := obj.Object["status"].(map[string]interface{})
	cluster := status["cluster"].(map[string]interface{})
	spec := obj.Object["spec"].(map[string]interface{})

	info.Version = spec["version"].(string)
	info.Instances = spec["instances"].(int64)
	info.ServerId = spec["baseServerId"].(int64)
	info.Secret = spec["secretName"].(string)
	info.StartTime = status["createTime"].(string)
	info.Status = cluster["status"].(string)
	info.OnlineInstances = cluster["onlineInstances"].(int64)
	info.Topology = "Cluster"
	if info.Instances == 1 {
		info.Topology = "Standalone"
	}
	info.Engine = playground.DefaultEngine
	info.Storage = 2
}
