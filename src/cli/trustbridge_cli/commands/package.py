"""
Package command - Generate Azure Managed Application packages.

This command creates a ZIP package containing ARM templates and UI definitions
for deploying the TrustBridge runtime as an Azure Managed Application.
"""

import json
import zipfile
from pathlib import Path
from typing import Optional

import typer
from typing_extensions import Annotated

from ..common import (
    console,
    success,
    error,
    info,
    warning,
    display_table,
    validate_url,
    TrustBridgeError,
    ValidationError,
)


def _generate_arm_template(
    asset_id: str,
    image: str,
    edc_endpoint: str,
) -> dict:
    """
    Generate ARM template for Azure Managed Application.

    Args:
        asset_id: Asset identifier
        image: Full Docker image name
        edc_endpoint: Control Plane endpoint

    Returns:
        ARM template dictionary
    """
    template = {
        "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
        "contentVersion": "1.0.0.0",
        "parameters": {
            "vmSize": {
                "type": "string",
                "defaultValue": "Standard_NC24ads_A100_v4",
                "allowedValues": [
                    "Standard_NC24ads_A100_v4",
                    "Standard_NC48ads_A100_v4",
                    "Standard_NC96ads_A100_v4",
                    "Standard_ND96asr_v4",
                ],
                "metadata": {
                    "description": "GPU VM size for model inference"
                },
            },
            "contractId": {
                "type": "string",
                "metadata": {
                    "description": "TrustBridge contract ID (required for authorization)"
                },
            },
            "location": {
                "type": "string",
                "defaultValue": "[resourceGroup().location]",
                "metadata": {
                    "description": "Location for all resources"
                },
            },
            "vmName": {
                "type": "string",
                "defaultValue": "trustbridge-runtime",
                "metadata": {
                    "description": "Virtual machine name"
                },
            },
        },
        "variables": {
            "assetId": asset_id,
            "edcEndpoint": edc_endpoint,
            "containerImage": image,
            "nicName": "[concat(parameters('vmName'), '-nic')]",
            "publicIPName": "[concat(parameters('vmName'), '-pip')]",
            "nsgName": "[concat(parameters('vmName'), '-nsg')]",
            "vnetName": "trustbridge-vnet",
            "subnetName": "default",
        },
        "resources": [
            # Network Security Group
            {
                "type": "Microsoft.Network/networkSecurityGroups",
                "apiVersion": "2021-02-01",
                "name": "[variables('nsgName')]",
                "location": "[parameters('location')]",
                "properties": {
                    "securityRules": [
                        {
                            "name": "AllowSentinelInbound",
                            "properties": {
                                "priority": 1000,
                                "protocol": "Tcp",
                                "access": "Allow",
                                "direction": "Inbound",
                                "sourceAddressPrefix": "*",
                                "sourcePortRange": "*",
                                "destinationAddressPrefix": "*",
                                "destinationPortRange": "8000",
                                "description": "Allow inbound to sentinel proxy",
                            },
                        },
                        {
                            "name": "DenyRuntimeInbound",
                            "properties": {
                                "priority": 1100,
                                "protocol": "Tcp",
                                "access": "Deny",
                                "direction": "Inbound",
                                "sourceAddressPrefix": "*",
                                "sourcePortRange": "*",
                                "destinationAddressPrefix": "*",
                                "destinationPortRange": "8081",
                                "description": "Block direct access to runtime",
                            },
                        },
                    ]
                },
            },
            # Public IP
            {
                "type": "Microsoft.Network/publicIPAddresses",
                "apiVersion": "2021-02-01",
                "name": "[variables('publicIPName')]",
                "location": "[parameters('location')]",
                "sku": {"name": "Standard"},
                "properties": {
                    "publicIPAllocationMethod": "Static",
                    "dnsSettings": {
                        "domainNameLabel": "[toLower(parameters('vmName'))]"
                    },
                },
            },
            # Virtual Network
            {
                "type": "Microsoft.Network/virtualNetworks",
                "apiVersion": "2021-02-01",
                "name": "[variables('vnetName')]",
                "location": "[parameters('location')]",
                "properties": {
                    "addressSpace": {"addressPrefixes": ["10.0.0.0/16"]},
                    "subnets": [
                        {
                            "name": "[variables('subnetName')]",
                            "properties": {"addressPrefix": "10.0.0.0/24"},
                        }
                    ],
                },
            },
            # Network Interface
            {
                "type": "Microsoft.Network/networkInterfaces",
                "apiVersion": "2021-02-01",
                "name": "[variables('nicName')]",
                "location": "[parameters('location')]",
                "dependsOn": [
                    "[resourceId('Microsoft.Network/publicIPAddresses', variables('publicIPName'))]",
                    "[resourceId('Microsoft.Network/virtualNetworks', variables('vnetName'))]",
                    "[resourceId('Microsoft.Network/networkSecurityGroups', variables('nsgName'))]",
                ],
                "properties": {
                    "ipConfigurations": [
                        {
                            "name": "ipconfig1",
                            "properties": {
                                "privateIPAllocationMethod": "Dynamic",
                                "publicIPAddress": {
                                    "id": "[resourceId('Microsoft.Network/publicIPAddresses', variables('publicIPName'))]"
                                },
                                "subnet": {
                                    "id": "[resourceId('Microsoft.Network/virtualNetworks/subnets', variables('vnetName'), variables('subnetName'))]"
                                },
                            },
                        }
                    ],
                    "networkSecurityGroup": {
                        "id": "[resourceId('Microsoft.Network/networkSecurityGroups', variables('nsgName'))]"
                    },
                },
            },
            # Virtual Machine
            {
                "type": "Microsoft.Compute/virtualMachines",
                "apiVersion": "2021-03-01",
                "name": "[parameters('vmName')]",
                "location": "[parameters('location')]",
                "dependsOn": [
                    "[resourceId('Microsoft.Network/networkInterfaces', variables('nicName'))]"
                ],
                "identity": {"type": "SystemAssigned"},
                "properties": {
                    "hardwareProfile": {"vmSize": "[parameters('vmSize')]"},
                    "osProfile": {
                        "computerName": "[parameters('vmName')]",
                        "adminUsername": "azureuser",
                        "linuxConfiguration": {
                            "disablePasswordAuthentication": True,
                            "ssh": {
                                "publicKeys": [
                                    {
                                        "path": "/home/azureuser/.ssh/authorized_keys",
                                        "keyData": "ssh-rsa AAAAB3... # PLACEHOLDER - REPLACE WITH REAL KEY",
                                    }
                                ]
                            },
                        },
                        "customData": "[base64(concat('#!/bin/bash\\nset -e\\n# TrustBridge initialization\\nexport TB_CONTRACT_ID=', parameters('contractId'), '\\nexport TB_ASSET_ID=', variables('assetId'), '\\nexport TB_EDC_ENDPOINT=', variables('edcEndpoint'), '\\n# Install Docker\\ncurl -fsSL https://get.docker.com | sh\\n# Pull and run container\\ndocker run -d --gpus all -p 8000:8000 -e TB_CONTRACT_ID -e TB_ASSET_ID -e TB_EDC_ENDPOINT ', variables('containerImage')))]",
                    },
                    "storageProfile": {
                        "imageReference": {
                            "publisher": "Canonical",
                            "offer": "0001-com-ubuntu-server-focal",
                            "sku": "20_04-lts-gen2",
                            "version": "latest",
                        },
                        "osDisk": {
                            "createOption": "FromImage",
                            "managedDisk": {"storageAccountType": "Premium_LRS"},
                        },
                    },
                    "networkProfile": {
                        "networkInterfaces": [
                            {
                                "id": "[resourceId('Microsoft.Network/networkInterfaces', variables('nicName'))]"
                            }
                        ]
                    },
                },
            },
        ],
        "outputs": {
            "hostname": {
                "type": "string",
                "value": "[reference(variables('publicIPName')).dnsSettings.fqdn]",
            },
            "sentinelEndpoint": {
                "type": "string",
                "value": "[concat('http://', reference(variables('publicIPName')).dnsSettings.fqdn, ':8000')]",
            },
        },
    }

    return template


def _generate_ui_definition() -> dict:
    """
    Generate createUiDefinition.json for Azure Portal.

    Returns:
        UI definition dictionary
    """
    ui_def = {
        "$schema": "https://schema.management.azure.com/schemas/0.1.2-preview/CreateUIDefinition.MultiVm.json#",
        "handler": "Microsoft.Azure.CreateUIDef",
        "version": "0.1.2-preview",
        "parameters": {
            "basics": [],
            "steps": [
                {
                    "name": "vmConfig",
                    "label": "Virtual Machine Configuration",
                    "elements": [
                        {
                            "name": "vmSize",
                            "type": "Microsoft.Compute.SizeSelector",
                            "label": "GPU VM Size",
                            "toolTip": "Select GPU VM size for inference workload",
                            "recommendedSizes": [
                                "Standard_NC24ads_A100_v4",
                                "Standard_NC48ads_A100_v4",
                            ],
                            "constraints": {
                                "allowedSizes": [
                                    "Standard_NC24ads_A100_v4",
                                    "Standard_NC48ads_A100_v4",
                                    "Standard_NC96ads_A100_v4",
                                    "Standard_ND96asr_v4",
                                ]
                            },
                            "osPlatform": "Linux",
                            "visible": True,
                        },
                        {
                            "name": "contractId",
                            "type": "Microsoft.Common.TextBox",
                            "label": "Contract ID",
                            "toolTip": "Your TrustBridge contract identifier",
                            "constraints": {
                                "required": True,
                                "regex": "^[a-zA-Z0-9_-]+$",
                                "validationMessage": "Contract ID must contain only alphanumeric characters, hyphens, and underscores",
                            },
                            "visible": True,
                        },
                    ],
                }
            ],
            "outputs": {
                "vmSize": "[steps('vmConfig').vmSize]",
                "contractId": "[steps('vmConfig').contractId]",
                "location": "[location()]",
            },
        },
    }

    return ui_def


def command(
    asset_id: Annotated[
        str,
        typer.Option(
            "--asset-id",
            help="Asset identifier",
        ),
    ],
    image: Annotated[
        str,
        typer.Option(
            "--image",
            help="Full Docker image name (registry/image:tag)",
        ),
    ],
    output_zip: Annotated[
        Path,
        typer.Option(
            "--output",
            "-o",
            help="Output ZIP file path",
        ),
    ] = Path("./trustbridge-app.zip"),
    edc_endpoint: Annotated[
        str,
        typer.Option(
            "--edc-endpoint",
            help="Control Plane endpoint URL",
        ),
    ] = "https://controlplane.trustbridge.io",
) -> None:
    """
    Generate Azure Managed Application package.

    This command creates a ZIP package containing:
      - mainTemplate.json: ARM template for deployment
      - createUiDefinition.json: Azure Portal UI definition

    The package can be published to Azure Marketplace or deployed
    directly as a Managed Application.

    Prerequisites:
        - Docker image must be built and pushed (trustbridge build)
        - Asset must be registered with Control Plane (trustbridge register)

    Examples:

        # Generate package with defaults
        trustbridge package --asset-id llama-3-70b-v1 \\
            --image myregistry.azurecr.io/trustbridge-runtime:latest

        # Custom output path
        trustbridge package --asset-id my-model \\
            --image myregistry.azurecr.io/trustbridge:v1.0 \\
            --output ./packages/my-model.zip

        # Custom Control Plane endpoint
        trustbridge package --asset-id my-model \\
            --image myregistry.azurecr.io/trustbridge:latest \\
            --edc-endpoint https://my-controlplane.example.com

    Package Structure:
        trustbridge-app.zip
        ├── mainTemplate.json       (ARM deployment template)
        └── createUiDefinition.json (Portal UI definition)

    Next Steps:
        1. Publish to Azure Marketplace, OR
        2. Deploy directly as Managed Application
        3. Consumers deploy via Azure Portal with their contract ID
    """
    try:
        # Validate inputs
        info("Validating inputs...")
        validate_url(edc_endpoint, require_https=True)

        if not image:
            raise ValidationError(
                "Docker image is required",
                details="Provide the full image name from trustbridge build",
            )

        # Display package parameters
        console.console.print()
        display_table(
            "Package Parameters",
            [
                ("Asset ID", asset_id),
                ("Docker Image", image),
                ("EDC Endpoint", edc_endpoint),
                ("Output ZIP", str(output_zip)),
            ],
        )
        console.console.print()

        info("Generating ARM template...")
        arm_template = _generate_arm_template(
            asset_id=asset_id,
            image=image,
            edc_endpoint=edc_endpoint,
        )

        info("Generating UI definition...")
        ui_definition = _generate_ui_definition()

        info("Creating package ZIP...")

        # Ensure output directory exists
        output_zip.parent.mkdir(parents=True, exist_ok=True)

        # Create ZIP package
        with zipfile.ZipFile(output_zip, "w", zipfile.ZIP_DEFLATED) as zipf:
            # Add ARM template
            zipf.writestr(
                "mainTemplate.json",
                json.dumps(arm_template, indent=2),
            )

            # Add UI definition
            zipf.writestr(
                "createUiDefinition.json",
                json.dumps(ui_definition, indent=2),
            )

        # Display results
        package_size = output_zip.stat().st_size
        console.console.print()
        display_table(
            "Package Results",
            [
                ("Package File", str(output_zip)),
                ("Package Size", f"{package_size / 1024:.1f} KB"),
                ("Files Included", "mainTemplate.json, createUiDefinition.json"),
            ],
        )

        success("Package created successfully!")
        console.console.print()
        info("Next steps:")
        console.console.print(
            "  1. Test the package locally or in a dev subscription"
        )
        console.console.print(
            "  2. Publish to Azure Marketplace Partner Center"
        )
        console.console.print(
            "  3. Consumers can deploy via Azure Portal with their contract ID"
        )

        warning(
            "Remember to update the SSH key placeholder in mainTemplate.json "
            "before publishing!"
        )

    except TrustBridgeError as e:
        error(e.message, details=e.details)
        raise typer.Exit(1)
    except Exception as e:
        error(f"Unexpected error during packaging", details=str(e))
        raise typer.Exit(1)
