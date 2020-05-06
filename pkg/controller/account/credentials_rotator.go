package account

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/go-logr/logr"
	awsv1alpha1 "github.com/openshift/aws-account-operator/pkg/apis/aws/v1alpha1"
	"github.com/openshift/aws-account-operator/pkg/awsclient"
	"github.com/openshift/aws-account-operator/pkg/controller/utils"
	"github.com/openshift/aws-account-operator/pkg/credentialwatcher"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// RotateCredentials update existing secret with new STS tokens and Singin URL
func (r *ReconcileAccount) RotateCredentials(reqLogger logr.Logger, awsSetupClient awsclient.Client, account *awsv1alpha1.Account, platformARNPrefix string) error {
	STSCredentialsSecretName := account.Name + credentialwatcher.STSCredentialsSuffix
	STSCredentialsSecretNamespace := account.Namespace

	reqLogger.Info(fmt.Sprintf("Rotating credentials for account %s secret %s", account.Name, STSCredentialsSecretName))

	//var awsAssumedRoleClient awsclient.Client
	var roleToAssume string

	if account.Spec.BYOC {
		roleToAssume = byocRole
	} else {
		roleToAssume = awsv1alpha1.AccountOperatorIAMRole
	}

	// Get STS user credentials
	STSCredentials, STSCredentialsErr := getStsCredentials(reqLogger, awsSetupClient, roleToAssume, account.Spec.AwsAccountID, platformARNPrefix)

	if STSCredentialsErr != nil {
		reqLogger.Info("RotateCredentials: Failed to get SRE admin STSCredentials from AWS api ", "Error", STSCredentialsErr.Error())
		return STSCredentialsErr
	}

	STSSecret := &corev1.Secret{}

	// If this secret doesn't exist go don't delete and just create
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: STSCredentialsSecretName, Namespace: awsv1alpha1.AccountCrNamespace}, STSSecret)

	// Return an error only if the error is not that the secret doesn't exist
	if err != nil {
		if !apierrors.IsNotFound(err) {
			errMsg := fmt.Sprintf("Error retrieving cli secret %s", STSCredentialsSecretName)
			reqLogger.Error(err, errMsg)
			return err
		}

	} else {
		// Delete the secret if there was no error
		err = r.Client.Delete(context.TODO(), STSSecret)

		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("Error deleting cli secret %s", STSCredentialsSecretName))
			return err
		}
	}

	secretName := fmt.Sprintf("%s-sre-cli-credentials", account.Name)
	secretData := map[string][]byte{
		"awsCredsSecretIDKey":     []byte(*STSCredentials.Credentials.AccessKeyId),
		"awsCredsSecretAccessKey": []byte(*STSCredentials.Credentials.SecretAccessKey),
		"awsCredsSessionToken":    []byte(*STSCredentials.Credentials.SessionToken),
	}

	STSCredentialsSecret := CreateSecret(secretName, STSCredentialsSecretNamespace, secretData)

	err = r.Client.Create(context.TODO(), STSCredentialsSecret)
	if err != nil {
		reqLogger.Error(err, fmt.Sprintf("Unable to update secret %s", STSSecret.Name))
		return err
	}

	// Set `status.RotateCredentials` to false now that they have been updated
	account.Status.RotateCredentials = false

	err = r.Client.Status().Update(context.TODO(), account)
	if err != nil {
		reqLogger.Error(err, fmt.Sprintf("RotateCredentials: Error updating account %s", account.Name))
		return err
	}

	reqLogger.Info(fmt.Sprintf("AWS STS and signin token rotated for account %s valid for %d", account.Name, credentialwatcher.STSCredentialsDuration-credentialwatcher.STSCredentialsThreshold))

	return nil
}

// RotateConsoleCredentials rotates STS credentials used to access the AWS console for a given account.
func (r *ReconcileAccount) RotateConsoleCredentials(reqLogger logr.Logger, awsSetupClient awsclient.Client, account *awsv1alpha1.Account, pc utils.AwsPlatformConfig) error {
	STSCredentialsSecretName := account.Name + credentialwatcher.STSCredentialsConsoleSuffix

	//var awsAssumedRoleClient awsclient.Client
	var roleToAssume string

	if account.Spec.BYOC {
		roleToAssume = byocRole
	} else {
		roleToAssume = awsv1alpha1.AccountOperatorIAMRole
	}

	// Get STS user credentials
	STSCredentials, STSCredentialsErr := getStsCredentials(reqLogger, awsSetupClient, roleToAssume, account.Spec.AwsAccountID, pc.ARNPrefix)

	if STSCredentialsErr != nil {
		reqLogger.Info("RotateCredentials: Failed to get SRE admin STSCredentials from AWS api ", "Error", STSCredentialsErr.Error())
		return STSCredentialsErr
	}

	STSUserName := account.Name + "-sts"

	IAMAdministratorPolicy := pc.ARNPrefix + utils.AWSIAMPolicyAdministrator

	IAMPolicy := sts.PolicyDescriptorType{Arn: &IAMAdministratorPolicy}

	IAMPolicyDescriptors := []*sts.PolicyDescriptorType{&IAMPolicy}

	SigninTokenDuration := int64(credentialwatcher.STSCredentialsDuration)

	// Create new awsClient with SRE IAM credentials so we can generate STS and Federation tokens from it
	SREAWSClient, err := awsclient.GetAWSClient(r.Client, awsclient.NewAwsClientInput{
		SecretName: account.Name + "-" + strings.ToLower(iamUserNameSRE) + "-secret",
		NameSpace:  awsv1alpha1.AccountCrNamespace,
		AwsRegion:  pc.ClientInput.AwsRegion,
	})
	if err != nil {
		reqLogger.Error(err, "RotateCredentials: Unable to create AWS conn with IAM user creds")
		return err
	}

	SREConsoleLoginURL, err := RequestSigninToken(reqLogger, SREAWSClient, &SigninTokenDuration, &STSUserName, IAMPolicyDescriptors, STSCredentials, pc.FederationConfig)
	if err != nil {
		reqLogger.Error(err, "RotateCredentials: Unable to create AWS signin token")
		return err
	}

	secretName := account.Name

	STSConsoleSecretName := fmt.Sprintf("%s-sre-console-url", secretName)
	STSConsoleSecretData := map[string][]byte{
		"aws_console_login_url": []byte(SREConsoleLoginURL),
	}

	userConsoleSecret := CreateSecret(STSConsoleSecretName, account.Namespace, STSConsoleSecretData)

	STSSecret := &corev1.Secret{}

	// If this secret doesn't exist go don't delete and just create
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: STSCredentialsSecretName, Namespace: awsv1alpha1.AccountCrNamespace}, STSSecret)

	// Return an error only if the error is not that the secret doesn't exist
	if err != nil {
		if !apierrors.IsNotFound(err) {
			errMsg := fmt.Sprintf("Error retrieving console secret %s", STSCredentialsSecretName)
			reqLogger.Error(err, errMsg)
			return err
		}

	} else {
		// Delete the secret if there was no error
		err = r.Client.Delete(context.TODO(), STSSecret)

		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("Error deleting console secret %s", STSCredentialsSecretName))
			return err
		}
	}

	err = r.Client.Create(context.TODO(), userConsoleSecret)

	if err != nil {
		reqLogger.Error(err, fmt.Sprintf("Unable to update secret %s", STSSecret.Name))
		return err
	}

	// Set `status.RotateCredentials` to false now that they ahve been updated
	account.Status.RotateConsoleCredentials = false

	err = r.Client.Status().Update(context.TODO(), account)
	if err != nil {
		reqLogger.Error(err, fmt.Sprintf("RotateCredentials: Error updating account %s", account.Name))
		return err
	}

	reqLogger.Info(fmt.Sprintf("AWS console URL rotated for account %s valid for %d", account.Name, credentialwatcher.STSCredentialsDuration-credentialwatcher.STSCredentialsThreshold))

	return nil

}
