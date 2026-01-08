// TrustBridge Azure Managed App - GPU VM Deployment Template
// This Bicep template deploys a GPU VM with sentinel + runtime containers
// for secure model inference with TrustBridge protection.

// ============================================================================
// Parameters
// ============================================================================

@description('Name prefix for all resources')
param namePrefix string = 'trustbridge'

@description('GPU VM size for model inference')
@allowed([
  'Standard_NC6s_v3'           // 1x V100 16GB - smaller models (7B)
  'Standard_NC24ads_A100_v4'   // 1x A100 80GB - production (70B)
  'Standard_ND96asr_v4'        // 8x A100 320GB - largest models
])
param vmSize string = 'Standard_NC24ads_A100_v4'

@description('TrustBridge contract ID (required)')
@minLength(4)
@maxLength(64)
param contractId string

@description('TrustBridge asset ID (set by provider)')
param assetId string

@description('TrustBridge EDC/Control Plane endpoint (set by provider)')
param edcEndpoint string

@description('Container image for sentinel (registry/image:tag)')
param sentinelImage string

@description('Container image for runtime (registry/image:tag)')
param runtimeImage string

@description('Azure Container Registry name for image pull')
param acrName string = ''

@secure()
@description('SSH public key for VM admin access')
param adminSshKey string

@description('Admin username for VM')
param adminUsername string = 'azureuser'

@description('Enable public IP for direct access')
param enablePublicIp bool = true

@description('Source IP addresses allowed for SSH access (CIDR notation)')
param sshAllowedSourceAddresses string = '*'

@description('Location for all resources')
param location string = resourceGroup().location

@description('Enable billing integration with Azure Marketplace')
param billingEnabled bool = true

@description('Log level for sentinel')
@allowed(['debug', 'info', 'warn', 'error'])
param logLevel string = 'info'

// ============================================================================
// Variables
// ============================================================================

var vmName = '${namePrefix}-vm'
var nsgName = '${namePrefix}-nsg'
var vnetName = '${namePrefix}-vnet'
var subnetName = '${namePrefix}-subnet'
var nicName = '${namePrefix}-nic'
var publicIpName = '${namePrefix}-pip'
var dnsLabelPrefix = '${namePrefix}-${uniqueString(resourceGroup().id)}'

// Cloud-init script content (base64 encoded in customData)
var cloudInitScript = loadTextContent('cloud-init/init.sh')

// Environment variables for cloud-init - uses string concatenation for proper interpolation
var cloudInitEnv = 'export TB_CONTRACT_ID="${contractId}"\nexport TB_ASSET_ID="${assetId}"\nexport TB_EDC_ENDPOINT="${edcEndpoint}"\nexport SENTINEL_IMAGE="${sentinelImage}"\nexport RUNTIME_IMAGE="${runtimeImage}"\nexport ACR_NAME="${acrName}"\nexport TB_BILLING_ENABLED="${string(billingEnabled)}"\nexport TB_LOG_LEVEL="${logLevel}"\n'

// Combined cloud-init with environment setup
var fullCloudInit = '#!/bin/bash\n${cloudInitEnv}\n${cloudInitScript}'

// ============================================================================
// Network Security Group
// ============================================================================

resource nsg 'Microsoft.Network/networkSecurityGroups@2023-09-01' = {
  name: nsgName
  location: location
  properties: {
    securityRules: [
      {
        name: 'AllowSentinelAPI'
        properties: {
          priority: 100
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '8000'
          description: 'Allow inbound traffic to TrustBridge Sentinel API'
        }
      }
      {
        name: 'AllowSentinelHealth'
        properties: {
          priority: 110
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '8001'
          description: 'Allow inbound traffic to health endpoint for load balancers'
        }
      }
      {
        name: 'DenyRuntimeDirect'
        properties: {
          priority: 200
          direction: 'Inbound'
          access: 'Deny'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '8081'
          description: 'Explicitly deny direct access to runtime port - SECURITY CRITICAL'
        }
      }
      {
        name: 'AllowSSH'
        properties: {
          priority: 300
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: sshAllowedSourceAddresses
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '22'
          description: 'Allow SSH for administration (restrict source IPs in production)'
        }
      }
    ]
  }
}

// ============================================================================
// Virtual Network
// ============================================================================

resource vnet 'Microsoft.Network/virtualNetworks@2023-09-01' = {
  name: vnetName
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: [
        '10.0.0.0/16'
      ]
    }
    subnets: [
      {
        name: subnetName
        properties: {
          addressPrefix: '10.0.0.0/24'
          networkSecurityGroup: {
            id: nsg.id
          }
        }
      }
    ]
  }
}

// ============================================================================
// Public IP (Conditional)
// ============================================================================

resource publicIp 'Microsoft.Network/publicIPAddresses@2023-09-01' = if (enablePublicIp) {
  name: publicIpName
  location: location
  sku: {
    name: 'Standard'
    tier: 'Regional'
  }
  properties: {
    publicIPAllocationMethod: 'Static'
    dnsSettings: {
      domainNameLabel: dnsLabelPrefix
    }
  }
}

// ============================================================================
// Network Interface
// ============================================================================

resource nic 'Microsoft.Network/networkInterfaces@2023-09-01' = {
  name: nicName
  location: location
  properties: {
    ipConfigurations: [
      {
        name: 'ipconfig1'
        properties: {
          privateIPAllocationMethod: 'Dynamic'
          subnet: {
            id: vnet.properties.subnets[0].id
          }
          publicIPAddress: enablePublicIp ? {
            id: publicIp.id
          } : null
        }
      }
    ]
  }
}

// ============================================================================
// GPU Virtual Machine
// ============================================================================

resource vm 'Microsoft.Compute/virtualMachines@2024-03-01' = {
  name: vmName
  location: location
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    hardwareProfile: {
      vmSize: vmSize
    }
    osProfile: {
      computerName: vmName
      adminUsername: adminUsername
      linuxConfiguration: {
        disablePasswordAuthentication: true
        ssh: {
          publicKeys: [
            {
              path: '/home/${adminUsername}/.ssh/authorized_keys'
              keyData: adminSshKey
            }
          ]
        }
        provisionVMAgent: true
      }
      customData: base64(fullCloudInit)
    }
    storageProfile: {
      imageReference: {
        publisher: 'Canonical'
        offer: '0001-com-ubuntu-server-jammy'
        sku: '22_04-lts-gen2'
        version: 'latest'
      }
      osDisk: {
        name: '${vmName}-osdisk'
        createOption: 'FromImage'
        managedDisk: {
          storageAccountType: 'Premium_LRS'
        }
        diskSizeGB: 128
        caching: 'ReadWrite'
      }
    }
    networkProfile: {
      networkInterfaces: [
        {
          id: nic.id
          properties: {
            primary: true
          }
        }
      ]
    }
    diagnosticsProfile: {
      bootDiagnostics: {
        enabled: true
      }
    }
  }
  tags: {
    'trustbridge-contract': contractId
    'trustbridge-asset': assetId
    'trustbridge-component': 'gpu-inference-vm'
  }
}

// ============================================================================
// Role Assignment: ACR Pull (Conditional)
// ============================================================================

// Reference to existing ACR (if provided)
resource acr 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = if (!empty(acrName)) {
  name: acrName
}

// AcrPull role definition ID
var acrPullRoleId = '7f951dda-4ed3-4680-a7ca-43fe172d538d'

resource acrRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(acrName)) {
  name: guid(acr.id, vm.id, acrPullRoleId)
  scope: acr
  properties: {
    principalId: vm.identity.principalId
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', acrPullRoleId)
    principalType: 'ServicePrincipal'
  }
}

// ============================================================================
// Outputs
// ============================================================================

output vmName string = vm.name
output vmId string = vm.id
output vmManagedIdentityPrincipalId string = vm.identity.principalId
output vmManagedIdentityTenantId string = vm.identity.tenantId

output vmPublicIp string = enablePublicIp ? publicIp.properties.ipAddress : ''
output vmFqdn string = enablePublicIp ? publicIp.properties.dnsSettings.fqdn : ''
output vmPrivateIp string = nic.properties.ipConfigurations[0].properties.privateIPAddress

output sentinelEndpoint string = enablePublicIp ? 'http://${publicIp.properties.dnsSettings.fqdn}:8000' : ''
output healthEndpoint string = enablePublicIp ? 'http://${publicIp.properties.dnsSettings.fqdn}:8001/health' : ''

output nsgId string = nsg.id
output vnetId string = vnet.id
output subnetId string = vnet.properties.subnets[0].id
