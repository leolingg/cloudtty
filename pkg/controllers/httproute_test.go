package controllers

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	cloudshellv1alpha1 "github.com/cloudtty/cloudtty/pkg/apis/cloudshell/v1alpha1"
	"github.com/cloudtty/cloudtty/pkg/constants"
	"github.com/cloudtty/cloudtty/pkg/utils/gclient"
)

func newHTTPRouteTestCloudshell(name string) *cloudshellv1alpha1.CloudShell {
	cloudshell := &cloudshellv1alpha1.CloudShell{}
	cloudshell.Name = name
	cloudshell.Namespace = "cloudtty-system"
	cloudshell.Spec.ExposeMode = cloudshellv1alpha1.ExposureHTTPRoute
	cloudshell.Spec.HTTPRouteConfig = &cloudshellv1alpha1.HTTPRouteConfig{
		GatewayName:      "cloudshell-gateway",
		GatewayNamespace: "gateway-system",
	}
	return cloudshell
}

func newHTTPRouteTestController(objects ...*gatewayv1.HTTPRoute) *Controller {
	scheme := gclient.NewSchema()
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objects {
		builder = builder.WithObjects(obj)
	}
	return &Controller{Client: builder.Build(), Scheme: scheme}
}

func getHTTPRoute(t *testing.T, c *Controller, key types.NamespacedName) *gatewayv1.HTTPRoute {
	t.Helper()
	route := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.TODO(), key, route); err != nil {
		t.Fatalf("failed to get httproute %v: %v", key, err)
	}
	return route
}

func TestHTTPRouteNamespacedName(t *testing.T) {
	cloudshell := newHTTPRouteTestCloudshell("demo")

	// default name and the cloudshell namespace
	key := HTTPRouteNamespacedName(cloudshell)
	if key.Name != constants.DefaultHTTPRouteName || key.Namespace != "cloudtty-system" {
		t.Errorf("expected default %s/cloudtty-system, got %s/%s", constants.DefaultHTTPRouteName, key.Namespace, key.Name)
	}

	// custom name and namespace
	cloudshell.Spec.HTTPRouteConfig.RouteName = "custom-route"
	cloudshell.Spec.HTTPRouteConfig.Namespace = "custom-ns"
	key = HTTPRouteNamespacedName(cloudshell)
	if key.Name != "custom-route" || key.Namespace != "custom-ns" {
		t.Errorf("expected custom-route/custom-ns, got %s/%s", key.Name, key.Namespace)
	}
}

func TestRuleMatchesPath(t *testing.T) {
	pathValue := "/apis/v1alpha1/cloudshell/demo"
	rule := gatewayv1.HTTPRouteRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{Value: &pathValue},
		}},
	}

	if !ruleMatchesPath(rule, pathValue) {
		t.Errorf("expected rule to match path %s", pathValue)
	}
	if ruleMatchesPath(rule, "/other/path") {
		t.Errorf("expected rule not to match a different path")
	}
	if ruleMatchesPath(gatewayv1.HTTPRouteRule{}, pathValue) {
		t.Errorf("expected empty rule not to match")
	}
}

func TestCreateHTTPRouteForCloudshell(t *testing.T) {
	ctx := context.TODO()

	// missing configuration options
	cloudshell := newHTTPRouteTestCloudshell("demo")
	cloudshell.Spec.HTTPRouteConfig = nil
	c := newHTTPRouteTestController()
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-0", cloudshell); err == nil {
		t.Errorf("expected error when httpRouteConfig is nil")
	}

	// missing gateway name
	cloudshell.Spec.HTTPRouteConfig = &cloudshellv1alpha1.HTTPRouteConfig{}
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-0", cloudshell); err == nil {
		t.Errorf("expected error when gateway name is empty")
	}

	// create the base httproute
	cloudshell = newHTTPRouteTestCloudshell("demo")
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-0", cloudshell); err != nil {
		t.Fatalf("failed to create httproute: %v", err)
	}

	key := HTTPRouteNamespacedName(cloudshell)
	route := getHTTPRoute(t, c, key)
	if len(route.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(route.Spec.Rules))
	}
	parent := route.Spec.ParentRefs[0]
	if string(parent.Name) != "cloudshell-gateway" || parent.Namespace == nil || string(*parent.Namespace) != "gateway-system" {
		t.Errorf("unexpected parentRefs: %+v", parent)
	}
	rule := route.Spec.Rules[0]
	if !ruleMatchesPath(rule, SetRouteRulePath(cloudshell)) {
		t.Errorf("rule does not match the cloudshell path")
	}
	if len(rule.Filters) == 0 || rule.Filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Errorf("expected URLRewrite filter on the rule")
	}
	if string(rule.BackendRefs[0].Name) != "worker-0" {
		t.Errorf("expected backend worker-0, got %s", rule.BackendRefs[0].Name)
	}

	// another cloudshell appends a rule to the shared httproute
	cloudshell2 := newHTTPRouteTestCloudshell("demo2")
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-1", cloudshell2); err != nil {
		t.Fatalf("failed to append rule to httproute: %v", err)
	}
	route = getHTTPRoute(t, c, key)
	if len(route.Spec.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(route.Spec.Rules))
	}
	if !ruleMatchesPath(route.Spec.Rules[1], SetRouteRulePath(cloudshell2)) {
		t.Errorf("appended rule does not match the second cloudshell path")
	}
	if len(route.Spec.Rules[1].Filters) == 0 || route.Spec.Rules[1].Filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Errorf("expected URLRewrite filter on the appended rule")
	}

	// rebinding the same cloudshell refreshes the backend instead of appending
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-2", cloudshell); err != nil {
		t.Fatalf("failed to refresh backend of httproute: %v", err)
	}
	route = getHTTPRoute(t, c, key)
	if len(route.Spec.Rules) != 2 {
		t.Fatalf("expected still 2 rules, got %d", len(route.Spec.Rules))
	}
	if string(route.Spec.Rules[0].BackendRefs[0].Name) != "worker-2" {
		t.Errorf("expected backend refreshed to worker-2, got %s", route.Spec.Rules[0].BackendRefs[0].Name)
	}
}

func TestRemoveCloudshellHTTPRouteRule(t *testing.T) {
	ctx := context.TODO()

	cloudshell := newHTTPRouteTestCloudshell("demo")
	cloudshell2 := newHTTPRouteTestCloudshell("demo2")
	c := newHTTPRouteTestController()

	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-0", cloudshell); err != nil {
		t.Fatalf("failed to create httproute: %v", err)
	}
	if err := c.CreateHTTPRouteForCloudshell(ctx, "worker-1", cloudshell2); err != nil {
		t.Fatalf("failed to append rule to httproute: %v", err)
	}

	key := HTTPRouteNamespacedName(cloudshell)

	// removing one cloudshell keeps the shared httproute with the other rule
	if err := c.removeCloudshellRoute(ctx, cloudshell); err != nil {
		t.Fatalf("failed to remove rule from httproute: %v", err)
	}
	route := getHTTPRoute(t, c, key)
	if len(route.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule left, got %d", len(route.Spec.Rules))
	}
	if !ruleMatchesPath(route.Spec.Rules[0], SetRouteRulePath(cloudshell2)) {
		t.Errorf("the remaining rule does not belong to the second cloudshell")
	}

	// removing the last cloudshell deletes the httproute
	if err := c.removeCloudshellRoute(ctx, cloudshell2); err != nil {
		t.Fatalf("failed to remove last rule from httproute: %v", err)
	}
	err := c.Get(ctx, key, &gatewayv1.HTTPRoute{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected httproute to be deleted, got err: %v", err)
	}
}
