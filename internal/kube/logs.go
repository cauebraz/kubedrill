package kube

import (
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
)

// QuietClientLogs stops client-go from writing to stderr through klog: the
// error handlers of apimachinery (discovery failures such as "couldn't get
// resource list for metrics.k8s.io/v1beta1") and the default handler of
// API server warnings. Errors still reach the caller as returned values.
// Importing klog to discard its output would add it to go.mod, so the
// throttling notices client-go prints after a wait of more than a second
// remain. Call it once from main.
func QuietClientLogs() {
	utilruntime.ErrorHandlers = []utilruntime.ErrorHandler{}
	rest.SetDefaultWarningHandlerWithContext(rest.NoWarnings{})
}
