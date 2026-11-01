// dev runs cmd/local -dev behind a reloading proxy at http://localhost:8100,
// with the Vite dev server for the setup page's assets. Go changes rebuild
// the server after the workflow analyzer passes, and template changes
// refresh the browser.
package main

import (
	"fmt"

	"github.com/eriicafes/go-dev"
	"github.com/eriicafes/go-dev/vite"
	"github.com/eriicafes/go-workflow/workflowdev"
)

func main() {
	session := dev.New()
	task := session.NewTask(dev.Cmd{
		Dir:     dev.Dir("../.."),
		Run:     dev.Package("./cmd/local"),
		Prepare: dev.Hooks(workflowdev.Check()),
		Args:    dev.Values("-dev"),
		Watch:   dev.Values("go.mod", "go.sum", "cmd/local", "store", "telegram", "web", "workflows"),
		// go-dev always skips .git and node_modules. Vite serves web/src and
		// writes web/dist itself.
		WatchExclude: dev.Values("web/dist", "web/src"),
		ServerAddr:   ":8100",
		Commands: dev.Commands(dev.Cmd{
			Run:   dev.Binary("pnpm"),
			Args:  dev.Values("dev"),
			Phase: dev.Before,
		}),
		Plugins: dev.Plugins(vite.Refresh{}),
	})
	session.Catch(task.Run())
	fmt.Printf("> LoveLoop dev server at %s\n", task.URL())
	session.Catch(session.Wait())
}
