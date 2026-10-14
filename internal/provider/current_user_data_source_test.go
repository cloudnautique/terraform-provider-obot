// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"obot": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, key := range []string{"OBOT_BASE_URL", "OBOT_TOKEN", "OBOT_TEST_EMAIL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s must be set; use make testacc-local for an isolated instance", key)
		}
	}
}

func TestAccCurrentUser_basic(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `provider "obot" {}
data "obot_current_user" "current" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.obot_current_user.current", tfjsonpath.New("email"), knownvalue.StringExact(os.Getenv("OBOT_TEST_EMAIL"))),
				statecheck.ExpectKnownValue("data.obot_current_user.current", tfjsonpath.New("id"), knownvalue.NotNull()),
			},
		}},
	})
}

func TestAccCurrentUser_invalidToken(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `provider "obot" {
  token = "invalid-test-token"
}
data "obot_current_user" "current" {}`,
			ExpectError: regexp.MustCompile("Unable to Connect to Obot"),
		}},
	})
}

func TestAccCurrentUser_multipleInstances(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("acceptance tests require TF_ACC=1")
	}
	var instances []struct {
		BaseURL string `json:"base_url"`
		Token   string `json:"token"`
		Email   string `json:"email"`
	}
	connections := os.Getenv("OBOT_TEST_INSTANCES")
	if connections == "" {
		t.Skip("OBOT_TEST_INSTANCES was not supplied; use OBOT_INSTANCES=2 with make testacc-local")
	}
	if err := json.Unmarshal([]byte(connections), &instances); err != nil {
		t.Fatal("OBOT_TEST_INSTANCES must contain JSON connection records")
	}
	if len(instances) < 2 {
		t.Skip("use OBOT_INSTANCES=2 to test independent provider configurations")
	}
	config := fmt.Sprintf(`
provider "obotfirst" {
  base_url = %[1]q
  token = %[2]q
}
provider "obotsecond" {
  base_url = %[3]q
  token = %[4]q
}
data "obot_current_user" "first" {
  provider = obotfirst
}
data "obot_current_user" "second" {
  provider = obotsecond
}
`, instances[0].BaseURL, instances[0].Token, instances[1].BaseURL, instances[1].Token)
	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		// The reattach-based acceptance harness does not isolate provider aliases.
		// Give each configuration its own factory key and protocol server.
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"obotfirst":  providerserver.NewProtocol6WithError(New("test")()),
			"obotsecond": providerserver.NewProtocol6WithError(New("test")()),
		},
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.obot_current_user.first", tfjsonpath.New("email"), knownvalue.StringExact(instances[0].Email)),
				statecheck.ExpectKnownValue("data.obot_current_user.second", tfjsonpath.New("email"), knownvalue.StringExact(instances[1].Email)),
			},
		}},
	})
}
