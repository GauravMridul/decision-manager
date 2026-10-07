package decision_manager_request_dto

// TriggerDecisionRequest represents the request body for triggering a decision
type TriggerDecisionRequest struct {
	ApplicationID           string `json:"Application_Id__c" binding:"required" example:"APP123456" description:"Partner application ID/Lead ID"`
	CustomerID              string `json:"customerId__c" example:"CUST123456" description:"Customer ID"`
	WorkflowID              string `json:"workflowId__c" example:"123e4567-e89b-12d3-a456-426614174000" description:"Workflow ID"`
	PartnerName             string `json:"LeadSource__c" binding:"required" example:"Samsung"`
	ProgramType             string `json:"Program_Type__c"  example:"Repeat"`
	BusinessType            string `json:"Business_Type__c" example:"Personal Loan"`
	SourcingProgram         string `json:"Sourcing_Program__c"  example:"Personal Loan"`
	LoanCategory            string `json:"Loan_Category__c" example:"Personal Loan"`
	CustomerType            string `json:"Customer_Type__c"  example:"Personal Loan"`
	ProductLine             string `json:"Product_Line__c"  example:"Personal Loan"`
	SalesChannelPartnerName string `json:"Sales_Channel_Partner_Name__c"`
	SourcingChannel         string `json:"Sourcing_Channel__c"`
	NameOfConsolidator      string `json:"Name_of_Consolidator__c"`
	LeadID                  string `json:"RecordId__c" binding:"required" example:"00Q9H00000DIPjdUAH" description:"Lead ID"`
	Stage                   string `json:"StageEvent__c" binding:"required" example:"Kyc" description:"Stage"`
}
