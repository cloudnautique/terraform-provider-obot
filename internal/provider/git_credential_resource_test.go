// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

const gitCredentialAddress = "obot_git_credential.test"

// Write-only attributes need Terraform 1.11.
var testAccWriteOnlyVersion = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(version.Must(version.NewVersion("1.11.0")))}

func testAccGitCredentialDestroy(t *testing.T) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		for _, item := range state.RootModule().Resources {
			if item.Type == "obot_git_credential" {
				_, err := testAccTunnelClient(t).GetGitCredential(t.Context(), item.Primary.ID)
				if !client.IsNotFound(err) {
					return fmt.Errorf("Git credential still exists or absence could not be verified: %v", err)
				}
			}
		}
		return nil
	}
}

func testAccGitCredentialConfig(name, token string) string {
	return fmt.Sprintf(`provider "obot" {}
resource "obot_git_credential" "test" {
  display_name = %[1]q
  host         = "github.com"
  %[2]s
}
data "obot_git_credential" "by_id" { id = obot_git_credential.test.id }
data "obot_git_credential" "by_name" { display_name = obot_git_credential.test.display_name }
data "obot_git_credentials" "all" { depends_on = [obot_git_credential.test] }
`, name, token)
}

// testAccGitCredentialMatches checks the credential's metadata in Obot.
func testAccGitCredentialMatches(t *testing.T, name string) statecheck.StateCheck {
	return tunnelStateCheck{address: gitCredentialAddress, check: func(ctx context.Context, values map[string]any) error {
		id, _ := values["id"].(string)
		credential, err := testAccTunnelClient(t).GetGitCredential(ctx, id)
		if err != nil {
			return err
		}
		if credential.DisplayName != name || credential.Host != "github.com" || !credential.TokenConfigured {
			return fmt.Errorf("credential in Obot = %+v", credential)
		}
		return nil
	}}
}

func TestAccGitCredential_lifecycle(t *testing.T) {
	name := "terraform-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks:   testAccWriteOnlyVersion,
		CheckDestroy:             testAccGitCredentialDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: testAccGitCredentialConfig(name, `token = "first-token"`),
				ConfigStateChecks: []statecheck.StateCheck{
					testAccGitCredentialMatches(t, name),
					statecheck.ExpectIdentity(gitCredentialAddress, map[string]knownvalue.Check{"id": knownvalue.NotNull()}),
					statecheck.CompareValuePairs("data.obot_git_credential.by_name", tfjsonpath.New("id"), gitCredentialAddress, tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.ExpectKnownValue("data.obot_git_credential.by_id", tfjsonpath.New("token_configured"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("data.obot_git_credential.by_id", tfjsonpath.New("used_by"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			{
				Config:            testAccGitCredentialConfig(name+"-renamed", `token = "second-token"`),
				ConfigStateChecks: []statecheck.StateCheck{testAccGitCredentialMatches(t, name+"-renamed")},
			},
			{
				Config: testAccGitCredentialConfig(name+"-renamed", "token_wo = \"third-token\"\n  token_wo_version = 1"),
				ConfigStateChecks: []statecheck.StateCheck{
					testAccGitCredentialMatches(t, name+"-renamed"),
					statecheck.ExpectKnownValue(gitCredentialAddress, tfjsonpath.New("token"), knownvalue.Null()),
					statecheck.ExpectKnownValue(gitCredentialAddress, tfjsonpath.New("token_wo"), knownvalue.Null()),
				},
			},
			{ResourceName: gitCredentialAddress, ImportState: true, ImportStateKind: resource.ImportCommandWithID, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"token", "token_wo", "token_wo_version"}},
			// Obot never returns the token, so an import plans to send the configured one.
			{
				ResourceName: gitCredentialAddress, ImportState: true, ImportStateKind: resource.ImportBlockWithResourceIdentity, ExpectNonEmptyPlan: true,
				ImportPlanChecks: resource.ImportPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(gitCredentialAddress, plancheck.ResourceActionUpdate)}},
			},
		},
	})
}

func TestAccGitCredential_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks:   testAccWriteOnlyVersion,
		Steps: []resource.TestStep{
			{Config: testAccGitCredentialConfig("invalid", ""), ExpectError: regexp.MustCompile(`token`)},
			{Config: testAccGitCredentialConfig("invalid", "token = \"a\"\n  token_wo = \"b\"\n  token_wo_version = 1"), ExpectError: regexp.MustCompile(`token`)},
			{Config: testAccGitCredentialConfig("invalid", `token_wo = "b"`), ExpectError: regexp.MustCompile(`token_wo_version`)},
			{Config: `provider "obot" {}
resource "obot_git_credential" "test" {
  display_name = "invalid"
  host         = "https://github.com"
  token        = "token"
}`, ExpectError: regexp.MustCompile(`lowercase host name`)},
		},
	})
}

// A private catalog source syncs only with the right credential.
func TestAccMCPCatalogSource_privateGit(t *testing.T) {
	initial, _ := testAccCatalogFixture(t)
	source, host, token := os.Getenv("OBOT_TEST_CATALOG_GIT_SOURCE"), os.Getenv("OBOT_TEST_CATALOG_GIT_HOST"), os.Getenv("OBOT_TEST_CATALOG_GIT_TOKEN")
	if source == "" || host == "" || token == "" {
		t.Fatal("the private Git fixture is required; use make test-acceptance-fresh")
	}
	conn := testAccTunnelClient(t)
	testAccCatalogSources(t, conn, initial, initial)
	config := func(host, token string, version int) string {
		return fmt.Sprintf(`provider "obot" {}
resource "obot_git_credential" "test" {
  display_name     = "Terraform private fixture"
  host             = %q
  token_wo         = %q
  token_wo_version = %d
}
resource "obot_mcp_catalog_source" "primary" {
  url               = %q
  git_credential_id = obot_git_credential.test.id
}
`, host, token, version, source)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		TerraformVersionChecks:   testAccWriteOnlyVersion,
		CheckDestroy: func(state *terraform.State) error {
			if err := testAccGitCredentialDestroy(t)(state); err != nil {
				return err
			}
			return testAccCatalogState(t.Context(), conn, "", map[string]string{initial: "Shipped Tool"}, initial)
		},
		Steps: []resource.TestStep{
			{Config: config("github.com", token, 1), ExpectError: regexp.MustCompile(`cannot\s+be\s+used\s+with\s+source\s+host`)},
			{Config: config(host, "wrong-token", 1), ExpectError: regexp.MustCompile(`MCP Catalog Source Failed to\s+Sync`)},
			{
				Config: config(host, token, 2),
				ConfigStateChecks: []statecheck.StateCheck{
					tunnelStateCheck{address: catalogSourceAddress, check: func(ctx context.Context, _ map[string]any) error {
						return testAccCatalogState(ctx, conn, "", map[string]string{initial: "Shipped Tool", source: "Private Tool"}, initial, source)
					}},
					statecheck.CompareValuePairs(catalogSourceAddress, tfjsonpath.New("git_credential_id"), gitCredentialAddress, tfjsonpath.New("id"), compare.ValuesSame()),
				},
			},
		},
	})
}
