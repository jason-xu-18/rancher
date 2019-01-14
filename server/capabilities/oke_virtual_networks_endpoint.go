package capabilities

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/oracle/oci-go-sdk/common"
	"github.com/oracle/oci-go-sdk/core"
	"github.com/oracle/oci-go-sdk/identity"
)

func NewOKEVirtualNetworksHandler() *okeVirtualNetworksHandler {
	return &okeVirtualNetworksHandler{}
}

type okeVirtualNetworksHandler struct {
}

type okeVirtualNetworksRequestBody struct {
	// credentials
	TenancyID   string `json:"tenancyID"`
	UserID      string `json:"userID"`
	Region      string `json:"region"`
	ApiKey      string `json:"apiKey"`
	FingerPrint string `json:"fingerPrint"`
}

type okeSubnet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type vcn struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Subnets []okeSubnet `json:"subnets"`
}

type compartment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	VCNS []vcn  `json:"vcns"`
}

type virtualNetworksResponseBody struct {
	Compartments []compartment `json:"compartments"`
}

func (g *okeVirtualNetworksHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writer.Header().Set("Content-Type", "application/json")

	var body okeVirtualNetworksRequestBody
	if err := extractRequestBody(writer, req, &body); err != nil {
		handleErr(writer, err)
		return
	}

	if err := validateOKEVirtualNetworksRequestBody(&body); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		handleErr(writer, err)
		return
	}

	tenancyID := body.TenancyID
	userID := body.UserID
	region := body.Region
	apiKey := body.ApiKey
	fingerPrint := body.FingerPrint

	provider := common.NewRawConfigurationProvider(tenancyID, userID, region, apiKey, fingerPrint, nil)

	c, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		handleErr(writer, fmt.Errorf("failed to configure oke oauth: %v", err))
		return
	}

	var networks []virtualNetworksResponseBody
	var compartments []compartment

	getRootCompartmentRequest := identity.GetCompartmentRequest{
		CompartmentId: &tenancyID,
	}

	rootCompartment, err := c.GetCompartment(ctx, getRootCompartmentRequest)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		handleErr(writer, fmt.Errorf("failed to get root compartment: %v", err))
		return
	}

	compartments = append(compartments, compartment{
		ID:   *rootCompartment.Id,
		Name: *rootCompartment.Name,
	})

	listChildCompartmentsRequest := identity.ListCompartmentsRequest{
		CompartmentId: &tenancyID,
	}

	childCompartments, err := c.ListCompartments(ctx, listChildCompartmentsRequest)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		handleErr(writer, fmt.Errorf("failed to get child compartments: %v", err))
		return
	}

	for _, item := range childCompartments.Items {
		compartments = append(compartments, compartment{
			ID:   *item.Id,
			Name: *item.Name,
		})
	}

	virtualNetworkClient, err := core.NewVirtualNetworkClientWithConfigurationProvider(provider)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		handleErr(writer, fmt.Errorf("failed to configure Virtual Network Client: %v", err))
		return
	}

	for compartmentKey, compartmentItem := range compartments {

		vcnRequest := core.ListVcnsRequest{
			CompartmentId: common.String(compartmentItem.ID),
		}

		vcnResponse, err := virtualNetworkClient.ListVcns(ctx, vcnRequest)
		if err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			handleErr(writer, fmt.Errorf("failed to list vcns: %v", err))
			return
		}

		for _, vcnCoreItem := range vcnResponse.Items {

			compartments[compartmentKey].VCNS = append(compartments[compartmentKey].VCNS, vcn{
				ID:   *vcnCoreItem.Id,
				Name: *vcnCoreItem.DisplayName,
			})

		}

		for vcnKey, vcnItem := range compartments[compartmentKey].VCNS {

			subnetRequest := core.ListSubnetsRequest{
				CompartmentId: common.String(compartmentItem.ID),
				VcnId:         common.String(vcnItem.ID),
			}

			subnetResponse, err := virtualNetworkClient.ListSubnets(ctx, subnetRequest)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				handleErr(writer, fmt.Errorf("failed to list Subnets: %v", err))
				return
			}

			for _, subnetItem := range subnetResponse.Items {
				compartments[compartmentKey].VCNS[vcnKey].Subnets = append(compartments[compartmentKey].VCNS[vcnKey].Subnets, subnet{
					ID:   *subnetItem.Id,
					Name: *subnetItem.DisplayName,
				})
			}

		}

	}

	networks = append(networks, compartments...)

	serialized, err := json.Marshal(networks)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		handleErr(writer, err)
		return
	}

	writer.Write(serialized)
}

func validateOKEVirtualNetworksRequestBody(body *virtualNetworksRequestBody) error {
	if body.TenancyID == "" {
		return fmt.Errorf("invalid TenancyID")
	}

	if body.UserID == "" {
		return fmt.Errorf("invalid UserID")
	}

	if body.Region == "" {
		return fmt.Errorf("invalid Region")
	}

	if body.ApiKey == "" {
		return fmt.Errorf("invalid ApiKey")
	}

	if body.fingerPrint == "" {
		return fmt.Errorf("invalid fingerPrint")
	}

	return nil
}
