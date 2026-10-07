pipeline {
    agent any
	
	parameters {
        gitParameter(
            name: 'DEPLOY_TAG',
            type: 'PT_TAG',
            branch: '',
            tagFilter: '*',
            sortMode: 'DESCENDING_SMART',  // Sort tags from latest to oldest
            defaultValue: '',
            selectedValue: 'NONE',
            description: 'Select a Git tag for deployment'
        )
    }

    environment {
        AWS_REGION = "ap-south-1"
        ECR_REPO_NAME = "decision-manager"
        EKS_CLUSTER_NAME = "prod-cluster"
        IMAGE_TAG = "${params.DEPLOY_TAG}"
		GITHUB_REPO = 'https://github.com/YOUR_USER/decision-manager.git'
		AWSACC_ID = credentials('sf-prod-acc-id')
		ROLE_TO_ASSUME = credentials('eks-cross-acc-role') // This is EKS account Role
    }
	

    stages {
        stage('Checkout Code') {
            steps {
                checkout scmGit(
                    branches: [[name: "${params.DEPLOY_TAG}"]],
                    extensions: [],
                    userRemoteConfigs: [[credentialsId: 'dm-esa', url: "${GITHUB_REPO}" ]]
                )
            }
        }
		stage('Assume Cross-Account Role') {
            steps {
                withCredentials([string(credentialsId: 'eks-cross-acc-role', variable: 'ROLE_TO_ASSUME')]) {
                    sh '''
                    echo "Assuming role: $ROLE_TO_ASSUME"

                    ASSUME_ROLE_OUTPUT=$(aws sts assume-role \
                      --role-arn "$ROLE_TO_ASSUME" \
                      --role-session-name jenkins-session)

                    export AWS_ACCESS_KEY_ID=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.AccessKeyId')
                    export AWS_SECRET_ACCESS_KEY=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SecretAccessKey')
                    export AWS_SESSION_TOKEN=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SessionToken')

                    # Save env vars for later use
                    echo "AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID" >> assume-role.env
                    echo "AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY" >> assume-role.env
                    echo "AWS_SESSION_TOKEN=$AWS_SESSION_TOKEN" >> assume-role.env
					aws sts get-caller-identity
                    '''
                }
            }
        }
		
        stage('Build and Push Docker Image') {
            steps {
				withCredentials([
					string(credentialsId: 'eks-cross-acc-role', variable: 'ROLE_TO_ASSUME'),
					usernamePassword(credentialsId: 'dm-esa', usernameVariable: 'GITHUB_USER', passwordVariable: 'GITHUB_TOKEN')
				]) {
                script {
                    sh '''
					echo "Assuming role: $ROLE_TO_ASSUME"
					ASSUME_ROLE_OUTPUT=$(aws sts assume-role \
                      --role-arn "$ROLE_TO_ASSUME" \
                      --role-session-name jenkins-session)
					export AWS_ACCESS_KEY_ID=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.AccessKeyId')
                    export AWS_SECRET_ACCESS_KEY=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SecretAccessKey')
                    export AWS_SESSION_TOKEN=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SessionToken')
					aws sts get-caller-identity --output text

                    aws ecr get-login-password --region $AWS_REGION | docker login --username AWS --password-stdin \
                    $AWSACC_ID.dkr.ecr.$AWS_REGION.amazonaws.com

                    docker build --no-cache --build-arg GITHUB_TOKEN=$GITHUB_TOKEN -t $ECR_REPO_NAME:$IMAGE_TAG .
                    docker tag $ECR_REPO_NAME:$IMAGE_TAG $AWSACC_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO_NAME:$IMAGE_TAG
                    docker push $AWSACC_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO_NAME:$IMAGE_TAG
                    '''
                }
            }
        }
		}

        stage('Deploy to EKS') {
    steps {
        withCredentials([string(credentialsId: 'eks-cross-acc-role', variable: 'ROLE_TO_ASSUME')]) {
            script {
                sh '''
                echo "Assuming role: $ROLE_TO_ASSUME"
                ASSUME_ROLE_OUTPUT=$(aws sts assume-role \
                  --role-arn "$ROLE_TO_ASSUME" \
                  --role-session-name jenkins-session)

                export AWS_ACCESS_KEY_ID=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.AccessKeyId')
                export AWS_SECRET_ACCESS_KEY=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SecretAccessKey')
                export AWS_SESSION_TOKEN=$(echo $ASSUME_ROLE_OUTPUT | jq -r '.Credentials.SessionToken')

                # Configure kubeconfig
                aws eks update-kubeconfig --name $EKS_CLUSTER_NAME --region $AWS_REGION --kubeconfig /var/lib/jenkins/.kube/config

                IMAGE="$AWSACC_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO_NAME:$IMAGE_TAG"

                sed "s|<IMAGE>|$IMAGE|g" deployment-prod.yml > deployment-prod-rendered.yml

                # Deploy to Kubernetes
                kubectl --kubeconfig /var/lib/jenkins/.kube/config apply -f deployment-prod-rendered.yml
                '''
            }
        }
    }
}


    }

    post {
        failure {
            echo '❌ Deployment failed!'
        }
        success {
            echo '✅ Successfully deployed to EKS!'
        }
    }
}


