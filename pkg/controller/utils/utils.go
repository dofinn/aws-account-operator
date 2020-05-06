package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"

	"github.com/go-logr/logr"
	awsv1alpha1 "github.com/openshift/aws-account-operator/pkg/apis/aws/v1alpha1"
	"github.com/openshift/aws-account-operator/pkg/awsclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	EmailID   = "osd-creds-mgmt"
	Finalizer = "finalizer.aws.managed.openshift.io"
	WaitTime  = 25

	// EnvDevMode is the name of the env var we set to run locally and to skip
	// initialization procedures that will error out and exit the operator.
	// ex: `FORCE_DEV_MODE=local operatorsdk up local`
	EnvDevMode = "FORCE_DEV_MODE"
)

// The JSON tags as captials due to requirements for the policydoc
type awsStatement struct {
	Effect    string                 `json:"Effect"`
	Action    []string               `json:"Action"`
	Resource  []string               `json:"Resource,omitempty"`
	Condition *awsv1alpha1.Condition `json:"Condition,omitempty"`
	Principal *awsv1alpha1.Principal `json:"Principal,omitempty"`
}

// DetectDevMode gets the environment variable to detect if we are running
// locally or (future) have some other environment-specific conditions.
var DetectDevMode string = strings.ToLower(os.Getenv(EnvDevMode))

type awsPolicy struct {
	Version   string
	Statement []awsStatement
}

// MarshalIAMPolicy converts a role CR into a JSON policy that is acceptable to AWS
func MarshalIAMPolicy(role awsv1alpha1.AWSFederatedRole) (string, error) {
	statements := []awsStatement{}

	for _, statement := range role.Spec.AWSCustomPolicy.Statements {
		statements = append(statements, awsStatement(statement))
	}

	// Create a aws policydoc formated struct
	policyDoc := awsPolicy{
		Version:   "2012-10-17",
		Statement: statements,
	}

	// Marshal policydoc to json
	jsonPolicyDoc, err := json.Marshal(&policyDoc)
	if err != nil {
		return "", err
	}

	return string(jsonPolicyDoc), nil
}

// GenerateAccountCR returns new account CR struct
func GenerateAccountCR(namespace string) *awsv1alpha1.Account {

	uuid := rand.String(6)
	accountName := EmailID + "-" + uuid

	return &awsv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accountName,
			Namespace: namespace,
		},
		Spec: awsv1alpha1.AccountSpec{
			AwsAccountID:       "",
			IAMUserSecret:      "",
			ClaimLink:          "",
			ClaimLinkNamespace: "",
		},
	}
}

// AddFinalizer adds a finalizer to an object
func AddFinalizer(object metav1.Object, finalizer string) {
	finalizers := sets.NewString(object.GetFinalizers()...)
	finalizers.Insert(finalizer)
	object.SetFinalizers(finalizers.List())
}

// LogAwsError formats and logs aws error and returns if err was an awserr
func LogAwsError(logger logr.Logger, errMsg string, customError error, err error) {
	if aerr, ok := err.(awserr.Error); ok {
		if customError == nil {
			customError = aerr
		}

		logger.Error(customError,
			fmt.Sprintf(`%s,
				AWS Error Code: %s,
				AWS Error Message: %s`,
				errMsg,
				aerr.Code(),
				aerr.Message()))
	}
}

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func Remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}

const (
	AWSRegionDefaultGlobal = "us-east-1"
	AWSRegionDefaultChina  = "cn-north-1"

	AWSSecretNameGlobal = "aws-account-operator-credentials"
	AWSSecretNameChina  = "aws-account-operator-china-credentials"

	AWSARNPrefixGlobal = "arn:aws:"
	AWSARNPrefixChina  = "arn:aws-cn:"

	AWSIAMPolicyAdministrator = "iam::aws:policy/AdministratorAccess"

	AWSFedEndpointURLGlobal = "https://signin.aws.amazon.com/federation"
	AWSFedEndpointURLChina  = "https://signin.amazonaws.cn/federation"

	AWSFedConsURLGlobal = "https://console.aws.amazon.com/"
	AWSFedConsURLChina  = "https://console.amazonaws.cn/"
)

// AwsPlatformConfig contains all required fields to service either AWS Global or AWS China.
type AwsPlatformConfig struct {
	ClientInput      awsclient.NewAwsClientInput
	ARNPrefix        string
	CoveredRegions   map[string]map[string]string
	FederationConfig AwsFederationConfig
}

// AwsFederationConfig consolidates EndpointURL and ConsoleURL into one structure that accounts
// for requirements of FederationConfig.
type AwsFederationConfig struct {
	EndpointURL string
	ConsoleURL  string
}

var (
	// AwsPlatformConfigGlobal provides a default struct with fields configured for AWS Global.
	AwsPlatformConfigGlobal = AwsPlatformConfig{
		ClientInput: awsclient.NewAwsClientInput{
			AwsRegion:  AWSRegionDefaultGlobal,
			SecretName: AWSSecretNameGlobal,
			NameSpace:  awsv1alpha1.AccountCrNamespace,
		},
		ARNPrefix:      AWSARNPrefixGlobal,
		CoveredRegions: awsv1alpha1.AWSRegionsGlobal,
		FederationConfig: AwsFederationConfig{
			EndpointURL: AWSFedEndpointURLGlobal,
			ConsoleURL:  AWSFedConsURLGlobal,
		},
	}

	// AwsPlatformConfigChina provides a default struct with fields configured for AWS China.
	AwsPlatformConfigChina = AwsPlatformConfig{
		ClientInput: awsclient.NewAwsClientInput{
			AwsRegion:  AWSRegionDefaultChina,
			SecretName: AWSSecretNameChina,
			NameSpace:  awsv1alpha1.AccountCrNamespace,
		},
		ARNPrefix:      AWSARNPrefixChina,
		CoveredRegions: awsv1alpha1.AWSRegionsChina,
		FederationConfig: AwsFederationConfig{
			EndpointURL: AWSFedEndpointURLChina,
			ConsoleURL:  AWSFedConsURLChina,
		},
	}
)
