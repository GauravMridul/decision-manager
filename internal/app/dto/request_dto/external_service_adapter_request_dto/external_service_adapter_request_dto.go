package external_service_adapter_request_dto

type ExternalServiceAdapterRequest struct {
	ApplicationID           string `json:"applicationId" binding:"required" example:"APP123456" description:"Partner application ID/Lead ID"`
	OldRefID                string `json:"oldRefId" example:"00Q9H00000DIPjdUAH" description:"Old Ref ID"`
	CustomerID              string `json:"customerId" binding:"required" example:"CUST123456" description:"Customer ID"`
	PartnerName             string `json:"partnerName" binding:"required" example:"Samsung"`
	ProgramType             string `json:"programType"  example:"Personal Loan"`
	BusinessType            string `json:"businessType"  example:"Personal Loan"`
	SourcingProgram         string `json:"sourcingProgram"  example:"Personal Loan"`
	LoanCategory            string `json:"loanCategory"  example:"Personal Loan"`
	CustomerType            string `json:"customerType"  example:"Personal Loan"`
	ProductLine             string `json:"productLine"  example:"Personal Loan"`
	SalesChannelPartnerName string `json:"salesChannelPartnerName"`
	SourcingChannel         string `json:"sourcingChannel"`
	NameOfConsolidator      string `json:"nameOfConsolidator"`
	SequenceID              string `json:"sequenceId" binding:"required" example:"SEQ123456" description:"Sequence ID"`
	SequenceString          string `json:"sequenceString" binding:"required" example:"{1,2;3;4}" description:"Sequence String"`
	Stage                   string `json:"stage" binding:"required" example:"Kyc" description:"Stage"`
	WorkflowID              string `json:"workflowId" binding:"required,uuid" example:"123e4567-e89b-12d3-a456-426614174000" description:"Workflow ID"`
}
