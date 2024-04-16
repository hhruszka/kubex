cnfexec is a command line application that executes commands in all containers in a given namespace or in a selected pod. 

### Usage
```
cnfexec [options] [args]

options:
  -c, --container string    a container name
  -h, --help                help for cnfexec-windows-amd64.exe
  -k, --kubeconfig string   (optional) absolute path to the kubeconfig file (default "C:\\Users\\hhruszka\\.kube\\config")
  -n, --namespace string    CNF namespace (default "default")
  -o, --output string       Output format: text, or json (default "text")
  -p, --pod string          a pod name, if not provided then all containers in a namespace will be enumerated.
  -v, --version             prints cnfexec-windows-amd64.exe version
```

### Examples

Execute 'ls' command on all pods' containers in a 'my-namespace' namespace:
```
cnfexec -n my-namespace ls
cnfexec -n my-namespace -- ls
cnfexec --namespace my-namespace -- ls
```

Execute 'find' command to search for setuid files on all pods' containers in a 'my-namespace' namespace:
```
cnfexec -n my-namespace -- sh -c 'find / -type f -perm /4000 -exec ls -l {} \; 2>/dev/null'
```

Execute 'find' command to search for *.cnf files on all pods' containers in a 'my-namespace' namespace:
```
cnfexec -n my-namespace -- sh -c 'find / -type f -name "*.cnf" -exec ls -l {} \; 2>/dev/null'
```

Execute shell script on all pods' containers in a 'my-namespace' namespace:
```
cat script.sh | cnfexec -n my-namespace 
# or
cat script.sh | cnfexec -n my-namespace sh
# or
cat script.sh | cnfexec -n my-namespace -- sh
# or
cat script.sh | cnfexec -n my-namespace -- bash
```
