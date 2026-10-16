// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

// These tests deliberately mutate installation-wide defaults and must not run on an arbitrary server.
func testAccDefaultsPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("acceptance tests require TF_ACC=1")
	}
	testAccPreCheck(t)
	if os.Getenv("OBOT_TEST_MANAGE_DEFAULTS") != "1" {
		t.Skip("default catalog/policy tests require an explicitly disposable instance; use make test-acceptance-fresh")
	}
}
func testAccCatalogFixture(t *testing.T) (string, string) {
	t.Helper()
	testAccDefaultsPreCheck(t)
	initial, next := os.Getenv("OBOT_TEST_CATALOG_SOURCE_INITIAL"), os.Getenv("OBOT_TEST_CATALOG_SOURCE_REPLACEMENT")
	if initial == "" || next == "" {
		t.Fatal("catalog fixtures are required; use make test-acceptance-fresh")
	}
	return initial, next
}
func testAccAPI(ctx context.Context, method, path string, body any, result any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(strings.TrimRight(os.Getenv("OBOT_BASE_URL"), "/"), "/api")
	req, err := http.NewRequestWithContext(ctx, method, base+"/api"+path, strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OBOT_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &client.HTTPError{Method: method, Path: path, StatusCode: response.StatusCode}
	}
	if result != nil {
		return json.NewDecoder(response.Body).Decode(result)
	}
	return nil
}
func testAccRuleConfig(name, subjects, resources string) string {
	return fmt.Sprintf(`provider "obot" {}
resource "obot_mcp_access_rule" "test" {
 display_name = %[1]q
 subjects = %[2]s
 resources = %[3]s
}
data "obot_mcp_access_rule" "test" { id = obot_mcp_access_rule.test.id }
data "obot_mcp_access_rules" "test" {}
`, name, subjects, resources)
}
func testAccRuleDestroy(t *testing.T) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		for _, item := range state.RootModule().Resources {
			if item.Type == "obot_mcp_access_rule" {
				_, err := testAccTunnelClient(t).GetAccessRule(t.Context(), item.Primary.Attributes["catalog_id"], item.Primary.ID)
				if !client.IsNotFound(err) {
					return fmt.Errorf("policy still exists or absence could not be verified: %v", err)
				}
			}
		}
		return nil
	}
}
func testAccRuleExists(t *testing.T, extra func(context.Context, map[string]any) error) statecheck.StateCheck {
	return tunnelStateCheck{address: "obot_mcp_access_rule.test", check: func(ctx context.Context, values map[string]any) error {
		id, _ := values["id"].(string)
		rule, err := testAccTunnelClient(t).GetAccessRule(ctx, "default", id)
		if err != nil {
			return err
		}
		if rule.DisplayName != values["display_name"] {
			return fmt.Errorf("policy name differs from API")
		}
		for key, refs := range map[string][]client.RuleReference{"subjects": rule.Subjects, "resources": rule.Resources} {
			actual := map[string]bool{}
			for _, ref := range refs {
				actual[ref.Type+":"+ref.ID] = true
			}
			vals, ok := values[key].([]any)
			if !ok || len(vals) != len(refs) {
				return fmt.Errorf("policy %s differ from API", key)
			}
			for _, val := range vals {
				ref, ok := val.(map[string]any)
				if !ok || !actual[fmt.Sprint(ref["type"])+":"+fmt.Sprint(ref["id"])] {
					return fmt.Errorf("policy %s differ from API", key)
				}
			}
		}
		if extra != nil {
			return extra(ctx, values)
		}
		return nil
	}}
}
func TestAccMCPAccessRule_basic(t *testing.T) {
	// Serial: these policies share a catalog with the default-source workflow.
	name := "terraform-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	wildcard := `[{type="selector",id="*"}]`
	var id string
	steps := []resource.TestStep{
		{Config: testAccRuleConfig(name, wildcard, wildcard), ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, func(_ context.Context, v map[string]any) error { id, _ = v["id"].(string); return nil }), statecheck.ExpectKnownValue("data.obot_mcp_access_rule.test", tfjsonpath.New("display_name"), knownvalue.StringExact(name)), statecheck.ExpectIdentity("obot_mcp_access_rule.test", map[string]knownvalue.Check{"id": knownvalue.NotNull()})}},
		{ResourceName: "obot_mcp_access_rule.test", ImportState: true, ImportStateIdFunc: func(_ *terraform.State) (string, error) { return "default/" + id, nil }, ImportStateVerify: true},
		{ResourceName: "obot_mcp_access_rule.test", ImportState: true, ImportStateKind: resource.ImportCommandWithID, ImportStateVerify: true},
		{ResourceName: "obot_mcp_access_rule.test", ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity},
		{Config: testAccRuleConfig(name+"-restricted", `[{type="obotGroup",id="admin"}]`, wildcard), ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, nil)}},
	}
	if os.Getenv("OBOT_TEST_CATALOG_SOURCE_INITIAL") != "" {
		config := testAccRuleConfig(name+"-specific", `[{type="user",id=data.obot_current_user.current.id}]`, `[{type="mcpServerCatalogEntry",id=data.obot_mcp_catalog.target.entries[0].id}]`) + `
data "obot_current_user" "current" {}
data "obot_mcp_catalog" "target" { id="default" }
`
		steps = append(steps, resource.TestStep{Config: config, ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, nil)}})
	}
	steps = append(steps, resource.TestStep{Config: testAccRuleConfig(name+"-empty", `[]`, `[]`), ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, nil)}})
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: testAccRuleDestroy(t), Steps: steps})
}
func TestAccMCPAccessRule_disappears(t *testing.T) {
	config := testAccRuleConfig("terraform-disappears-"+acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum), `[{type="selector",id="*"}]`, `[{type="selector",id="*"}]`)
	// Data sources cannot read a disappeared policy, so use the resource-only portion.
	config = strings.Split(config, "data ")[0]
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: testAccRuleDestroy(t), Steps: []resource.TestStep{
		{Config: config, ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, func(ctx context.Context, v map[string]any) error {
			id, _ := v["id"].(string)
			return testAccTunnelClient(t).DeleteAccessRule(ctx, "default", id)
		})}, ExpectNonEmptyPlan: true},
		{Config: config, ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, nil)}},
	}})
}
func TestAccMCPAccessRule_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: testAccRuleConfig("invalid", `[{type="selector",id="everyone"}]`, `[{type="selector",id="*"}]`), ExpectError: regexp.MustCompile("Invalid MCP Access Rule Reference")},
		{Config: testAccRuleConfig("invalid", `[{type="selector",id="*"}]`, `[{type="mcpCatalog",id="default"}]`), ExpectError: regexp.MustCompile("Invalid MCP Access Rule Reference")},
		{Config: `provider "obot" {}
data "obot_mcp_access_rule" "missing" { id="acr1-does-not-exist" }`, ExpectError: regexp.MustCompile("Error Reading MCP Access Rule")},
	}})
}
func TestAccMCPAccessRule_everythingImportAndRemove(t *testing.T) {
	testAccDefaultsPreCheck(t)
	conn := testAccTunnelClient(t)
	original, err := conn.GetAccessRule(t.Context(), "default", "acr1-everything")
	if err != nil {
		t.Fatal(err)
	}
	// The control rule proves deletion does not remove unrelated policies.
	control, err := conn.CreateAccessRule(t.Context(), "default", client.AccessRuleManifest{DisplayName: "terraform-control", Subjects: []client.RuleReference{{Type: "obotGroup", ID: "admin"}}, Resources: []client.RuleReference{{Type: "selector", ID: "*"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.DeleteAccessRule(context.Background(), "default", control.ID); err != nil {
			t.Error(err)
		}
	})
	refsHCL := func(refs []client.RuleReference) string {
		parts := []string{}
		for _, ref := range refs {
			parts = append(parts, fmt.Sprintf("{type=%q,id=%q}", ref.Type, ref.ID))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	config := testAccRuleConfig(original.DisplayName, refsHCL(original.Subjects), refsHCL(original.Resources)) + `
import {
 to=obot_mcp_access_rule.test
 id="default/acr1-everything"
}
`
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: testAccRuleDestroy(t), Steps: []resource.TestStep{
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("obot_mcp_access_rule.test", plancheck.ResourceActionNoop)}}, ConfigStateChecks: []statecheck.StateCheck{testAccRuleExists(t, func(ctx context.Context, v map[string]any) error {
			actual, err := conn.GetAccessRule(ctx, "default", original.ID)
			if err != nil {
				return err
			}
			if v["id"] != original.ID || !reflect.DeepEqual(original.AccessRuleManifest, actual.AccessRuleManifest) {
				return fmt.Errorf("import changed the existing Everything policy")
			}
			return nil
		})}},
		{Config: `provider "obot" {}
data "obot_mcp_access_rules" "remaining" {}`, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("obot_mcp_access_rule.test", plancheck.ResourceActionDestroy)}}, Check: func(_ *terraform.State) error {
			_, err := conn.GetAccessRule(t.Context(), "default", original.ID)
			if !client.IsNotFound(err) {
				return fmt.Errorf("Everything was not removed: %v", err)
			}
			if err := testAccRestartObot(t, conn); err != nil {
				return err
			}
			_, err = conn.GetAccessRule(t.Context(), "default", original.ID)
			if !client.IsNotFound(err) {
				return fmt.Errorf("Everything reappeared after restart: %v", err)
			}
			_, err = conn.GetAccessRule(t.Context(), "default", control.ID)
			return err
		}},
	}})
}

const catalogSourceAddress = "obot_mcp_catalog_source.primary"

func testAccCatalogConfig(primary, extra string) string {
	config := fmt.Sprintf(`provider "obot" {}
resource "obot_mcp_catalog_source" "primary" { url = %q }
`, primary)
	if extra != "" {
		config += fmt.Sprintf(`resource "obot_mcp_catalog_source" "extra" { url = %q }
`, extra)
	}
	return config
}

// testAccCatalogSources makes the default catalog hold exactly the given
// sources, in the state a UI user would leave it, and restores the shipped
// source when the test ends.
func testAccCatalogSources(t *testing.T, conn *client.Client, initial string, sources ...string) {
	t.Helper()
	set := func(ctx context.Context, sources []string) error {
		_, err := conn.ModifyCatalogSources(ctx, func(s *client.CatalogSources) {
			s.Sources = sources
			s.GitCredentialIDs = map[string]string{}
		})
		return err
	}
	ctx, cancel := context.WithTimeout(t.Context(), catalogSyncTimeout)
	defer cancel()
	if err := set(ctx, sources); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), catalogSyncTimeout)
		defer cancel()
		if err := set(ctx, []string{initial}); err != nil {
			t.Error(err)
		}
	})
}

// testAccCatalogState checks the default catalog's sources, in order, and
// that each source's entries, and the API-created entry, are present.
func testAccCatalogState(ctx context.Context, conn *client.Client, manualID string, want map[string]string, order ...string) error {
	catalog, err := conn.GetCatalog(ctx, "default")
	if err != nil {
		return err
	}
	if catalog.IsSyncing || len(catalog.SyncErrors) > 0 {
		return fmt.Errorf("catalog has not completed a successful sync: %v", catalog.SyncErrors)
	}
	if !reflect.DeepEqual(catalog.Sources, order) {
		return fmt.Errorf("sources = %v, want %v", catalog.Sources, order)
	}
	entries, err := conn.ListCatalogEntries(ctx, "default")
	if err != nil {
		return err
	}
	found := map[string]string{}
	foundManual := manualID == ""
	for _, entry := range entries {
		if entry.ID == manualID {
			foundManual = true
		} else if entry.SourceURL != "" {
			found[entry.SourceURL] = entry.Manifest.Name
		}
	}
	if !foundManual {
		return fmt.Errorf("the API-created entry was removed")
	}
	if !reflect.DeepEqual(found, want) {
		return fmt.Errorf("source entries = %v, want %v", found, want)
	}
	return nil
}

func testAccCatalogExtra(t *testing.T) string {
	t.Helper()
	extra := os.Getenv("OBOT_TEST_CATALOG_SOURCE_EXTRA")
	if extra == "" {
		t.Fatal("catalog fixtures are required; use make test-acceptance-fresh")
	}
	return extra
}

func testAccManualEntry(t *testing.T) string {
	t.Helper()
	var manual struct {
		ID string `json:"id"`
	}
	if err := testAccAPI(t.Context(), "POST", "/mcp-catalogs/default/entries", map[string]any{"name": "Terraform manual entry", "shortDescription": "API-created control", "description": "Preserved when sources change", "icon": "test", "runtime": "npx", "npxConfig": map[string]string{"package": "@example/terraform-fixture"}}, &manual); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			err := testAccAPI(ctx, "DELETE", "/mcp-catalogs/default/entries/"+manual.ID, nil, nil)
			if err == nil || client.IsNotFound(err) {
				return
			}
			var httpErr *client.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
				t.Error(err)
				return
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				t.Error(err)
				return
			case <-timer.C:
			}
		}
	})
	return manual.ID
}

// The shipped source stands in for one an administrator added in the UI.
func TestAccMCPCatalogSource_leavesOtherSources(t *testing.T) {
	initial, next := testAccCatalogFixture(t)
	extra := testAccCatalogExtra(t)
	conn := testAccTunnelClient(t)
	testAccCatalogSources(t, conn, initial, initial)
	manual := testAccManualEntry(t)
	check := func(want map[string]string, order ...string) []statecheck.StateCheck {
		return []statecheck.StateCheck{tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
			return testAccCatalogState(ctx, conn, manual, want, order...)
		}}}
	}
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: func(_ *terraform.State) error {
		if err := testAccCatalogState(t.Context(), conn, manual, map[string]string{initial: "Shipped Tool"}, initial); err != nil {
			return fmt.Errorf("destroy changed a source Terraform does not manage: %w", err)
		}
		if err := testAccRestartObot(t, conn); err != nil {
			return err
		}
		catalog, err := conn.GetCatalog(t.Context(), "default")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(catalog.Sources, []string{initial}) {
			return fmt.Errorf("startup changed catalog sources: %v", catalog.Sources)
		}
		return nil
	}, Steps: []resource.TestStep{
		{
			Config: testAccCatalogConfig(next, extra),
			ConfigStateChecks: []statecheck.StateCheck{
				tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
					catalog, err := conn.GetCatalog(ctx, "default")
					if err != nil {
						return err
					}
					// Both resources apply concurrently, so their order may vary.
					if len(catalog.Sources) != 3 || catalog.Sources[0] != initial || !slices.Contains(catalog.Sources, next) || !slices.Contains(catalog.Sources, extra) {
						return fmt.Errorf("sources = %v, want the UI source first and both Terraform sources", catalog.Sources)
					}
					return testAccCatalogState(ctx, conn, manual, map[string]string{initial: "Shipped Tool", next: "Replacement Tool", extra: "Extra Tool"}, catalog.Sources...)
				}},
				statecheck.ExpectKnownValue(catalogSourceAddress, tfjsonpath.New("id"), knownvalue.StringExact(next)),
				statecheck.ExpectIdentity(catalogSourceAddress, map[string]knownvalue.Check{"url": knownvalue.StringExact(next)}),
			},
		},
		{Config: testAccCatalogConfig(next, ""), ConfigStateChecks: check(map[string]string{initial: "Shipped Tool", next: "Replacement Tool"}, initial, next)},
		{Config: testAccCatalogConfig(next, ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{ResourceName: catalogSourceAddress, ImportState: true, ImportStateKind: resource.ImportCommandWithID, ImportStateId: next, ImportStateVerify: true},
		{ResourceName: catalogSourceAddress, ImportState: true, ImportStateKind: resource.ImportBlockWithID, ImportStateId: next},
		{ResourceName: catalogSourceAddress, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity},
	}})
}

func TestAccMCPCatalogSource_adoptExisting(t *testing.T) {
	initial, next := testAccCatalogFixture(t)
	conn := testAccTunnelClient(t)
	testAccCatalogSources(t, conn, initial, initial, next)
	adopted := testAccCatalogConfig(initial, "") + fmt.Sprintf(`import {
 to = %s
 id = %q
}
`, catalogSourceAddress, initial)
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: func(_ *terraform.State) error {
		return testAccCatalogState(t.Context(), conn, "", map[string]string{next: "Replacement Tool"}, next)
	}, Steps: []resource.TestStep{
		{Config: testAccCatalogConfig(initial, ""), ExpectError: regexp.MustCompile(`MCP Catalog Source Already\s+Exists`)},
		{Config: adopted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(catalogSourceAddress, plancheck.ResourceActionNoop)}}, ConfigStateChecks: []statecheck.StateCheck{tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
			return testAccCatalogState(ctx, conn, "", map[string]string{initial: "Shipped Tool", next: "Replacement Tool"}, initial, next)
		}}}},
	}})
}

func TestAccMCPCatalogSource_removedInUI(t *testing.T) {
	initial, next := testAccCatalogFixture(t)
	conn := testAccTunnelClient(t)
	testAccCatalogSources(t, conn, initial, initial)
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: testAccCatalogConfig(next, ""), ConfigStateChecks: []statecheck.StateCheck{tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
			_, err := conn.ModifyCatalogSources(ctx, removeSource(next))
			return err
		}}}, ExpectNonEmptyPlan: true},
		{Config: testAccCatalogConfig(next, ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(catalogSourceAddress, plancheck.ResourceActionCreate)}}, ConfigStateChecks: []statecheck.StateCheck{tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
			return testAccCatalogState(ctx, conn, "", map[string]string{initial: "Shipped Tool", next: "Replacement Tool"}, initial, next)
		}}}},
	}})
}

func TestAccCurrentAuthProvider_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: `provider "obot" {}
data "obot_current_auth_provider" "current" {}`, ConfigStateChecks: []statecheck.StateCheck{
			statecheck.ExpectKnownValue("data.obot_current_auth_provider.current", tfjsonpath.New("id"), knownvalue.StringExact("local-auth-provider")),
			statecheck.ExpectKnownValue("data.obot_current_auth_provider.current", tfjsonpath.New("namespace"), knownvalue.NotNull()),
		}},
	}})
}

// Restart only the container explicitly supplied by the fresh disposable runner.
func testAccRestartObot(t *testing.T, conn *client.Client) error {
	t.Helper()
	container := os.Getenv("OBOT_TEST_CONTAINER")
	if container == "" {
		return nil
	} // External opt-in runs cannot restart a server they do not own.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "restart", container).Run(); err != nil {
		return fmt.Errorf("restarting owned acceptance container: %w", err)
	}
	for {
		current, err := conn.GetCatalog(ctx, "default")
		if err == nil && !current.IsSyncing {
			if err := conn.Validate(ctx); err == nil {
				_, err = conn.RefreshCatalog(ctx, "default", current.LastSynced)
				return err
			}
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("waiting for restarted Obot: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
