package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	exec2 "k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/homedir"
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
)

var ExitCodes map[int]string = map[int]string{
	-1:  "Internal app error",
	0:   "Success",
	1:   "General error, unspecified error",
	2:   "Misuse of shell builtins",
	126: "Command cannot execute",
	127: "Command not found",
	128: "Invalid argument to exit",
	//130: "Script terminated by Control-C (SIGINT)",
	255: "Exit status out of range",
	// Signal based exit codes (128+n)
	129: "Fatal error signal 1 (SIGHUP)",
	130: "Fatal error signal 2 (SIGINT)",
	131: "Fatal error signal 3 (SIGQUIT)",
	132: "Fatal error signal 4 (SIGILL)",
	133: "Fatal error signal 5 (SIGTRAP)",
	134: "Fatal error signal 6 (SIGABRT/SIGIOT)",
	135: "Fatal error signal 7 (SIGBUS)",
	136: "Fatal error signal 8 (SIGFPE)",
	137: "Fatal error signal 9 (SIGKILL)",
	138: "Fatal error signal 10 (SIGUSR1)",
	139: "Fatal error signal 11 (SIGSEGV)",
	140: "Fatal error signal 12 (SIGUSR2)",
	141: "Fatal error signal 13 (SIGPIPE)",
	142: "Fatal error signal 14 (SIGALRM)",
	143: "Fatal error signal 15 (SIGTERM)",
	// Add more signal based codes as needed
}

func getExitCode(err error) (int, string) {
	var e exec2.CodeExitError
	if !errors.As(err, &e) {
		return -1, ""
	}
	if _, ok := ExitCodes[e.Code]; !ok {
		return -1, ""
	}
	return e.Code, ExitCodes[e.Code]
}

func getExitCodeDescription(code int) string {
	if _, ok := ExitCodes[code]; !ok {
		return ""
	}
	return ExitCodes[code]
}

func Init() {
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

func getPods(clientset *kubernetes.Clientset, namespace string, options metaV1.ListOptions) ([]corev1.Pod, error) {
	var pods *corev1.PodList
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), options)
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

func getDeployments(clientset *kubernetes.Clientset, namespace string) (*v1.DeploymentList, error) {
	var deployments *v1.DeploymentList
	deployments, err := clientset.AppsV1().Deployments(namespace).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return deployments, nil
}

func getStatefulSets(clientset *kubernetes.Clientset, namespace string) (*v1.StatefulSetList, error) {
	var statefulSets *v1.StatefulSetList
	statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return statefulSets, nil
}

func exec(clientset *kubernetes.Clientset, config *rest.Config, namespace string, podName string, containerName string, cmd []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, tty bool) (int, error) {

	//command := []string{cmd}

	req := clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       tty,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		if debug {
			fmt.Printf("[-] Execution failed with error code %d\n", err)
		}
		return -1, err
	}

	err = executor.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    false,
	})

	//fmt.Println(stdout)
	var execError exec2.CodeExitError
	if err != nil && !errors.As(err, &execError) {
		return -1, err
	} else if err != nil && errors.As(err, &execError) {
		return execError.Code, err
	}
	return 0, nil
}

func Exec(clientset *kubernetes.Clientset, config *rest.Config, namespace string, podName string, containerName string, args []string, stdin io.Reader) {
	var stdout, stderr bytes.Buffer

	retCode, err := exec(clientset, config, namespace, podName, containerName, args, stdin, &stdout, &stderr, false)
	fmt.Printf("CONTAINER: %s/%s\n", podName, containerName)
	fmt.Printf("Returned exit code: %d [%s]\n", retCode, getExitCodeDescription(retCode))
	if err != nil {
		fmt.Printf("Returned error: %s\n", err.Error())
	}
	if stdout.Len() > 0 {
		fmt.Printf("Standard output:\n%s\n", stdout.String())
	} else {
		fmt.Println("Standard output:")
	}
	if stderr.Len() > 0 {
		fmt.Printf("Standard error:\n%s\n", stderr.String())
	} else {
		fmt.Println("Standard error:")
	}
}

func run(args []string) error {
	Init()

	//Prepare to capture stdin
	var stdinBuf bytes.Buffer

	if pod != "" {
		if fi, err := os.Stdin.Stat(); err == nil {
			if (fi.Mode() & os.ModeCharDevice) == 0 {
				_, err = io.Copy(&stdinBuf, os.Stdin)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to read stdin: %v\n", err)
					os.Exit(1)
				}
			}
		}

		if stdinBuf.Len() > 0 && len(args) == 0 {
			// no command to pipe to has been providing defaulting to shell
			args = []string{"sh"}
		}
		if stdinBuf.Len() > 40 {
			fmt.Printf("PIPED COMMAND: %s ... too long to display\n", string(stdinBuf.Bytes()[:40]))
		} else {
			fmt.Printf("PIPED COMMAND: %s\n", strings.Trim(string(stdinBuf.Bytes()), "\n"))
		}
		fmt.Printf("COMMAND: %q\n\n", args)
	}

	switch {
	case pod != "" && container == "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		if _pod.Status.Phase == "Running" {
			for _, _container := range _pod.Spec.Containers {
				// each execution of command will empty stdin therefore
				// we need to preserve it and recreate for each iteration
				streamedCmd := bytes.NewBuffer(stdinBuf.Bytes())
				Exec(clientset, config, namespace, _pod.Name, _container.Name, args, streamedCmd)
				fmt.Println()
			}
		}
	case pod != "" && container != "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if _pod.Status.Phase != "Running" {
			fmt.Printf("Pod %s is not in Running phase\n", pod)
			os.Exit(1)
		}

		Exec(clientset, config, namespace, pod, container, args, &stdinBuf)
	case pod == "" && container == "":
		pods, err := getPods(clientset, namespace, metaV1.ListOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("Found %d pods in %s namespace\n\n", len(pods), namespace)
		for _, _pod := range pods {
			//fmt.Printf("Pod %s has %d containers: \n", _pod.Name, len(_pod.Spec.Containers))
			for _, _container := range _pod.Spec.Containers {
				fmt.Printf("%s/%s\n", _pod.Name, _container.Name)
			}
			fmt.Println()
		}
	}
	return nil
}

func main() {
	var cmd = &cobra.Command{
		Use:   "k8sexec",
		Short: "k8sexec is a command line application that executes commands in containers",
		Long:  ``,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args)
		},
	}

	if home := homedir.HomeDir(); home != "" {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", "", "absolute path to the kubeconfig file")
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "CNF namespace")
	cmd.Flags().StringVarP(&pod, "pod", "p", "", "a pod name, if not provided then all containers in a namespace will be enumerated.")
	cmd.Flags().StringVarP(&container, "container", "c", "", "a container name")
	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "debug")

	// Disable automatic printing of usage when an error occurs
	cmd.SilenceUsage = true

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

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
