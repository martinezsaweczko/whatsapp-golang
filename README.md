# Go Api Template

Template for building a REST API in Go with a clean architecture, configuration management, logging, and observability.


##So far
* Create a simple HTTP server that listens for incoming requests and responds with a message.

##TODO - Next
* Finish configuration management (authorization)




## How to Manage Go Version on Linux

### Install goenv

**Step 1: Install dependencies**

```bash
sudo apt update
sudo apt install -y git build-essential
```

**Step 2: Clone goenv**

```bash
git clone https://github.com/go-nv/goenv.git ~/.goenv
```

**Step 3: Add it to your shell**

Add the following to `~/.bashrc` or `~/.zshrc`:

```bash
export GOENV_ROOT="$HOME/.goenv"
export PATH="$GOENV_ROOT/bin:$PATH"
eval "$(goenv init -)"
```

Reload the shell:

```bash
source ~/.bashrc
```

### Using goenv

**List available Go versions**

```bash
goenv install -l
```

Example output:

```
1.20.14
1.21.11
1.22.6
1.23.3
```

**Install a version**

```bash
goenv install 1.22.6
```

**Set global version**

```bash
goenv global 1.22.6
goenv rehash
```

Check the version:

```bash
go version
```

**Set version per project**

Inside your project directory:

```bash
goenv local 1.22.6
```

This creates a `.go-version` file in your project.