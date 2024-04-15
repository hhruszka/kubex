package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8sexec/k8sexec"
	"os"
	"path/filepath"
	"strings"
)

// App global variables
var (
	config    *rest.Config
	clientset *kubernetes.Clientset
)

// CLI options variables
var (
	kubeconfig string
	namespace  string
	pod        string
	container  string
	debug      bool
	format     string
)

func k8sInit() {
	var err error

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

type EnumerationStatus struct {
	Stdin     string                     `json:"Stdin"`
	Args      []string                   `json:"Args"`
	Namespace string                     `json:"Namespace"`
	Statuses  []*k8sexec.ExecutionStatus `json:"Statuses"`
}

func NewEnumerationStatus(pipeCommand string, command []string, namespace string) *EnumerationStatus {
	if len(pipeCommand) > 40 {
		pipeCommand = fmt.Sprintf("%s... too long", pipeCommand[:40])
	}
	return &EnumerationStatus{Stdin: pipeCommand, Args: command, Namespace: namespace}
}

func NewExecutionStatus(pod string, container string, retCode int, error string, stdout string, stderr string) *k8sexec.ExecutionStatus {
	return &k8sexec.ExecutionStatus{Pod: pod, Container: container, RetCode: retCode, Error: strings.Split(error, "\n"), Stdout: strings.Split(stdout, "\n"), Stderr: strings.Split(stderr, "\n")}
}

func run(args []string) error {
	k8sInit()

	//Prepare to capture stdin
	var stdinBuf bytes.Buffer

	k8s, err := k8sexec.NewK8SExec(kubeconfig, namespace)
	if err != nil {
		return err
	}

	if fi, err := os.Stdin.Stat(); err == nil {
		if (fi.Mode() & os.ModeCharDevice) == 0 {
			_, err = io.Copy(&stdinBuf, os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read stdin: %v\n", err)
				os.Exit(1)
			}
		}
	}

	if stdinBuf.Len() == 0 && len(args) == 0 {
		return errors.New("No commands provided either by stdin or arguments.")
	}

	if stdinBuf.Len() > 0 && len(args) == 0 {
		// no command to pipe has been providing defaulting to shell
		args = []string{"sh"}
	}

	enumStatus := NewEnumerationStatus(stdinBuf.String(), args, namespace)
	switch {
	case pod != "" && container == "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		if _pod.Status.Phase == "Running" {
			for _, _container := range _pod.Spec.Containers {
				// each execution of command will empty stdin therefore
				// we need to preserve it and recreate for each iteration
				streamedCmd := bytes.NewBuffer(stdinBuf.Bytes())

				status := k8s.Exec(_pod.Name, _container.Name, args, streamedCmd)
				enumStatus.Statuses = append(enumStatus.Statuses, status)
			}
		}
	case pod != "" && container != "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if _pod.Status.Phase != "Running" {
			fmt.Printf("Pod %s is not in Running phase\n", pod)
			os.Exit(1)
		}

		status := k8s.Exec(pod, container, args, &stdinBuf)
		enumStatus.Statuses = append(enumStatus.Statuses, status)
	case pod == "" && container == "":
		pods, err := k8s.GetPods(metaV1.ListOptions{})
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		for _, _pod := range pods {
			if _pod.Status.Phase == "Running" {
				for _, _container := range _pod.Spec.Containers {
					// each execution of command will empty stdin therefore
					// we need to preserve it and recreate for each iteration
					streamedCmd := bytes.NewBuffer(stdinBuf.Bytes())
					status := k8s.Exec(_pod.Name, _container.Name, args, streamedCmd)
					enumStatus.Statuses = append(enumStatus.Statuses, status)
				}
			}
		}
	}

	switch format {
	case "json":
		jsonBuff, err := json.MarshalIndent(enumStatus, "", "    ")
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		fmt.Println(string((jsonBuff)))
	case "text":
		fmt.Printf("STDIN COMMAND: %s\n", enumStatus.Stdin)
		fmt.Printf("COMMAND: %q\n\n", enumStatus.Args)
		fmt.Printf("Namespace: %s\n", enumStatus.Namespace)
		for _, status := range enumStatus.Statuses {
			fmt.Printf("CONTAINER: %s/%s\n", status.Pod, status.Container)
			fmt.Printf("Returned exit code: %d [%s]\n", status.RetCode, k8sexec.GetExitCodeDescription(status.RetCode))
			if strings.Trim(strings.Join(status.Error, "\n"), "\n") != "" {
				fmt.Printf("Returned error: %s\n", strings.Join(status.Error, "\n"))
			}
			fmt.Printf("Standard output:\n%s", strings.Join(status.Stdout, "\n"))
			fmt.Printf("Standard error:\n%s", strings.Join(status.Stderr, "\n"))
			fmt.Println()
		}
	}

	return nil
}

var cmd = &cobra.Command{
	Use:   "k8sexec [flags] [args]",
	Short: "k8sexec is a command line application that executes commands in containers",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return run(args)
	},
}

func init() {
	if home := homedir.HomeDir(); home != "" {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", "", "absolute path to the kubeconfig file")
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "CNF namespace")
	cmd.Flags().StringVarP(&pod, "pod", "p", "", "a pod name, if not provided then all containers in a namespace will be enumerated.")
	cmd.Flags().StringVarP(&container, "container", "c", "", "a container name")
	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "debug")
	cmd.Flags().StringVarP(&format, "output", "o", "text", "Output format: text, or json")

	// Disable automatic printing of usage when an error occurs
	cmd.SilenceUsage = true

	// support for '--'
	cmd.Flags().SetInterspersed(false)

	// Custom PreRunE to check for parse errors
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// This checks for any parse errors
		if err := cmd.ParseFlags(args); err != nil {
			return err
		}
		return nil
	}

	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		// When a non-existing option is invoked, print the usage
		if err := c.Usage(); err != nil {
			fmt.Fprintf(os.Stderr, "Error printing usage: %v\n", err)
		}
		// Return the original error to stop execution
		return err
	})
}

func Execute() error {
	return cmd.Execute()
}
