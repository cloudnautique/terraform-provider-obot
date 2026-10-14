// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = &ObotProvider{}

// ObotProvider implements the Obot Terraform provider.
type ObotProvider struct {
	version string
}

func (p *ObotProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "obot"
	resp.Version = p.version
}

func (p *ObotProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Obot infrastructure with Terraform. This provider is under development; API configuration, resources, and data sources are not yet available.",
	}
}

func (p *ObotProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
	// API client configuration will be added with the first Obot resource.
}

func (p *ObotProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *ObotProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

// New returns a factory for the Obot provider at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ObotProvider{
			version: version,
		}
	}
}
